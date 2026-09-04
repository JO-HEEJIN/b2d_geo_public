// Package landsales는 국토부 토지매매 실거래(RTMSDataSvcLandTrade)를
// land_sales에 월 단위로 적재한다 (Phase 7-1 일부).
// 원칙: 사실 저장만. 지번이 마스킹된 원자료 특성상 위치 정밀도는
// EMD_CENTROID 등급으로 기록하고, 좌표는 parcels 기반 법정동 중심점을 쓴다
// (해당 시도 미적재 시 NULL). 정밀 좌표 추정·보간 금지 (지시서).
package landsales

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/korean"
)

type item struct {
	DealYear   string `xml:"dealYear"`
	DealMonth  string `xml:"dealMonth"`
	DealDay    string `xml:"dealDay"`
	DealAmount string `xml:"dealAmount"` // 만원, 쉼표 포함
	DealArea   string `xml:"dealArea"`
	Jimok      string `xml:"jimok"`
	LandUse    string `xml:"landUse"`
	SggCd      string `xml:"sggCd"`
	UmdNm      string `xml:"umdNm"`
	Jibun      string `xml:"jibun"`
}

type resp struct {
	Header struct {
		ResultCode string `xml:"resultCode"`
	} `xml:"header"`
	Body struct {
		Items      []item `xml:"items>item"`
		TotalCount int    `xml:"totalCount"`
	} `xml:"body"`
}

// Stats는 월 적재 결과다.
type Stats struct {
	Fetched   int // 원천 행
	Inserted  int
	NoEmd     int // 법정동 매핑 실패 (스킵)
	NoGeom    int // 중심점 없음 (geom NULL 적재)
	Ambiguous int // 동명 중복 매핑 (스킵)
}

// Load는 시군구(lawd 5자리) × 계약월(ym YYYYMM)을 멱등 재적재한다.
func Load(ctx context.Context, pool *pgxpool.Pool, apiKey, entranceDir, lawd, ym string) (*Stats, error) {
	emdMap, ambiguous, err := buildEmdMap(entranceDir, lawd)
	if err != nil {
		return nil, fmt.Errorf("emd map: %w", err)
	}

	u := fmt.Sprintf("https://apis.data.go.kr/1613000/RTMSDataSvcLandTrade/getRTMSDataSvcLandTrade?serviceKey=%s&LAWD_CD=%s&DEAL_YMD=%s&numOfRows=9999",
		url.QueryEscape(apiKey), lawd, ym)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; b2d-etl/0.1)") // data.go.kr WAF
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("molit request failed: %s",
			strings.ReplaceAll(strings.ReplaceAll(err.Error(), apiKey, "***"), url.QueryEscape(apiKey), "***"))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var rp resp
	if err := xml.Unmarshal(body, &rp); err != nil {
		return nil, fmt.Errorf("molit xml: %w", err)
	}
	if rp.Header.ResultCode != "000" {
		return nil, fmt.Errorf("molit resultCode=%s", rp.Header.ResultCode)
	}

	stats := &Stats{Fetched: len(rp.Body.Items)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 멱등: 해당 시군구×월 기존 행 삭제 후 재삽입
	monthStart := ym[:4] + "-" + ym[4:6] + "-01"
	if _, err := tx.Exec(ctx,
		`DELETE FROM land_sales WHERE emd_code LIKE $1 || '%' AND source='molit_rt'
		 AND contract_date >= $2::date AND contract_date < ($2::date + interval '1 month')`,
		lawd, monthStart); err != nil {
		return nil, err
	}

	centroids := map[string]any{} // bcode -> geom WKB or nil
	for _, it := range rp.Body.Items {
		umd := strings.TrimSpace(it.UmdNm)
		bcode, ok := emdMap[umd]
		if !ok {
			if ambiguous[umd] {
				stats.Ambiguous++
			} else {
				stats.NoEmd++
			}
			continue
		}
		amount, err1 := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(it.DealAmount), ",", ""), 10, 64)
		area, err2 := strconv.ParseFloat(strings.TrimSpace(it.DealArea), 64)
		y, m, d := strings.TrimSpace(it.DealYear), strings.TrimSpace(it.DealMonth), strings.TrimSpace(it.DealDay)
		if err1 != nil || err2 != nil || area <= 0 || y == "" || m == "" || d == "" {
			stats.NoEmd++ // 파싱 불가 행
			continue
		}
		date := fmt.Sprintf("%s-%02s-%02s", y, m, d)

		if _, seen := centroids[bcode]; !seen {
			var geom *string
			err := tx.QueryRow(ctx,
				`SELECT ST_AsEWKT(ST_PointOnSurface(ST_Collect(geom))) FROM parcels WHERE bcode=$1`,
				bcode).Scan(&geom)
			if err != nil || geom == nil {
				centroids[bcode] = nil
			} else {
				centroids[bcode] = *geom
			}
		}
		geomVal := centroids[bcode]
		if geomVal == nil {
			stats.NoGeom++
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO land_sales (emd_code, jibun_partial, contract_date, area_sqm,
			    price_total, use_zone, land_category, geom, location_precision, source_version)
			VALUES ($1,$2,$3::date,$4,$5,$6,$7,
			        CASE WHEN $8::text IS NULL THEN NULL ELSE ST_GeomFromEWKT($8) END,
			        'EMD_CENTROID', now()::date)`,
			bcode, strings.TrimSpace(it.Jibun), date, area, amount*10000,
			strings.TrimSpace(it.LandUse), strings.TrimSpace(it.Jimok), geomVal); err != nil {
			return stats, err
		}
		stats.Inserted++
	}
	if err := tx.Commit(ctx); err != nil {
		return stats, err
	}
	return stats, nil
}

// buildEmdMap은 주소 출입구 전체분(txt, CP949, |구분 19컬럼)에서
// 해당 시군구의 읍면동명 -> 법정동코드(10) 매핑을 만든다.
// 같은 이름이 서로 다른 코드로 두 번 나오면 모호 처리한다.
func buildEmdMap(dir, lawd string) (map[string]string, map[string]bool, error) {
	zips, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(zips) == 0 {
		return nil, nil, fmt.Errorf("no entrance zips in %s", dir)
	}
	emd := map[string]string{}
	ambiguous := map[string]bool{}
	dec := korean.EUCKR.NewDecoder()
	for _, zp := range zips {
		zr, err := zip.OpenReader(zp)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(f.Name, ".txt") {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return nil, nil, err
			}
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for sc.Scan() {
				raw := sc.Bytes()
				line, err := dec.Bytes(raw)
				if err != nil {
					continue
				}
				parts := strings.Split(string(line), "|")
				if len(parts) < 5 {
					continue
				}
				bcode := parts[1]
				if len(bcode) != 10 || !strings.HasPrefix(bcode, lawd) {
					continue
				}
				name := strings.TrimSpace(parts[4])
				if name == "" {
					continue
				}
				if prev, ok := emd[name]; ok && prev != bcode {
					ambiguous[name] = true
					delete(emd, name)
					continue
				}
				if !ambiguous[name] {
					emd[name] = bcode
				}
			}
			rc.Close()
		}
		zr.Close()
	}
	return emd, ambiguous, nil
}

var _ = os.Getenv // (예약: 향후 옵션)
