// Package priceindex는 한국부동산원 R-ONE "(월) 용도지역별 지가변동률" 포털
// JSON export를 land_price_index에 적재한다 (Phase 7-1 일부).
//
// 원칙(지시서): 사실 저장만. 원자료의 (시군구 × 용도지역 × 월) 변동률을 그대로 적재하고,
// 용도지역 분류는 재분류하지 않는다. 격차율/보정/평가/판단 계산 없음.
// 집계행(전국/수도권/지방/대도시/시지역/군지역)·과거 행정명"(구)…"·시군구 미매핑 행은 스킵한다.
package priceindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sheetFile은 포털 export 구조 {"sheet":{"1":{"data":{ "<row>": {"0..14": "값"} }}}}.
type sheetFile struct {
	Sheet map[string]struct {
		Data map[string]map[string]string `json:"data"`
	} `json:"sheet"`
}

// sidoFull은 파일의 시도 약칭 → admin_areas 정식명. 여기 없는 값(전국/수도권/지방/
// 대도시/시지역/군지역/(구)*/"지역")은 집계·레거시라 적재 대상이 아니다(스킵).
var sidoFull = map[string]string{
	"서울": "서울특별시", "부산": "부산광역시", "대구": "대구광역시", "인천": "인천광역시",
	"광주": "광주광역시", "대전": "대전광역시", "울산": "울산광역시", "세종": "세종특별자치시",
	"경기": "경기도", "강원": "강원특별자치도", "충북": "충청북도", "충남": "충청남도",
	"전북": "전북특별자치도", "전남": "전라남도", "경북": "경상북도", "경남": "경상남도",
	"제주": "제주특별자치도",
}

// Stats는 적재 결과다.
type Stats struct {
	DataRows   int // 데이터 행 (헤더 제외)
	Records    int // 적재된 (시군구×용도지역×월) 레코드
	Skipped    int // 집계/레거시/미매핑 행
	EmptyCells int // 값 없는/파싱 불가 셀
}

// Load는 포털 JSON을 파싱해 land_price_index에 upsert한다(PK 충돌 시 갱신, 멱등).
func Load(ctx context.Context, pool *pgxpool.Pool, jsonPath string) (*Stats, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", jsonPath, err)
	}
	var sf sheetFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	sheet, ok := sf.Sheet["1"]
	if !ok || sheet.Data == nil {
		return nil, fmt.Errorf("sheet.1.data 없음")
	}

	// 1) 헤더 행에서 컬럼("5".."14") → 월(YYYYMM) 매핑.
	// 헤더는 다중 행(col "0"=="No"): 월 행 / "변동률" 행 / "%" 행. map 순회는 무순서이므로
	// 첫 "No" 행에서 멈추면 안 된다 — parseMonth가 실제 월을 뽑은 행만 채택한다.
	colMonth := map[string]string{}
	for _, row := range sheet.Data {
		if row["0"] != "No" {
			continue
		}
		for c := 5; c <= 14; c++ {
			key := strconv.Itoa(c)
			if m := parseMonth(row[key]); m != "" {
				colMonth[key] = m
			}
		}
		if len(colMonth) > 0 {
			break
		}
	}
	if len(colMonth) == 0 {
		return nil, fmt.Errorf("헤더 월 라벨을 찾지 못함 (col0=\"No\" 행 없음)")
	}

	// 2) 크로스워크: admin_areas → 5자리 시군구코드
	xwalk, err := buildCrosswalk(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("crosswalk: %w", err)
	}

	stats := &Stats{}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	for _, row := range sheet.Data {
		if row["0"] == "No" {
			continue // 헤더 행
		}
		stats.DataRows++
		full, ok := sidoFull[strings.TrimSpace(row["1"])]
		if !ok {
			stats.Skipped++ // 집계/레거시 시도
			continue
		}
		si, gu := strings.TrimSpace(row["2"]), strings.TrimSpace(row["3"])
		if strings.HasPrefix(si, "(구)") || strings.HasPrefix(gu, "(구)") {
			stats.Skipped++
			continue
		}
		name := full + " " + si
		if gu != "" && gu != si {
			name += " " + gu // 시 + 자치구
		}
		code, ok := xwalk[name]
		if !ok {
			stats.Skipped++
			continue
		}
		useZone := strings.TrimSpace(row["4"])
		if useZone == "" {
			stats.Skipped++
			continue
		}
		for col, month := range colMonth {
			val := strings.TrimSpace(row[col])
			if val == "" {
				stats.EmptyCells++
				continue
			}
			rate, err := strconv.ParseFloat(val, 64)
			if err != nil {
				stats.EmptyCells++
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO land_price_index (sigungu_code, use_zone_class, month, rate_pct, source, source_version)
				VALUES ($1,$2,$3,$4,'reb_rone',now()::date)
				ON CONFLICT (sigungu_code, use_zone_class, month)
				DO UPDATE SET rate_pct=EXCLUDED.rate_pct, source_version=EXCLUDED.source_version`,
				code, useZone, month, rate); err != nil {
				return stats, err
			}
			stats.Records++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return stats, err
	}
	return stats, nil
}

// parseMonth은 "2025년 9월" → "202509". 형식 불일치면 "".
func parseMonth(s string) string {
	y, rest, ok := strings.Cut(strings.TrimSpace(s), "년")
	if !ok {
		return ""
	}
	mm := strings.TrimSuffix(strings.TrimSpace(rest), "월")
	yi, err1 := strconv.Atoi(strings.TrimSpace(y))
	mi, err2 := strconv.Atoi(strings.TrimSpace(mm))
	if err1 != nil || err2 != nil || mi < 1 || mi > 12 {
		return ""
	}
	return fmt.Sprintf("%04d%02d", yi, mi)
}

// buildCrosswalk은 admin_areas(법정동, name="시도 시군구 [자치구] 읍면동")에서
// "시도 시군구"와 "시도 시 자치구" 앞토큰 조합 → 5자리 시군구코드 맵을 만든다.
// 파일은 평시군구면 "시도 시군구", 자치구면 "시도 시 자치구"로 질의하므로 둘 다 등록한다.
func buildCrosswalk(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT substr(bcode,1,5), split_part(name,' ',1), split_part(name,' ',2), split_part(name,' ',3)
		 FROM admin_areas WHERE bcode IS NOT NULL AND name <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var code, t1, t2, t3 string
		if err := rows.Scan(&code, &t1, &t2, &t3); err != nil {
			return nil, err
		}
		if t1 == "" || t2 == "" {
			continue
		}
		m[t1+" "+t2] = code
		if t3 != "" {
			m[t1+" "+t2+" "+t3] = code
		}
	}
	return m, rows.Err()
}
