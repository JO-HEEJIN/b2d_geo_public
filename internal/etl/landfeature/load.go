// Package landfeature는 토지특성정보(NSDI AL_D195, 시도별×날짜별 중첩 zip) →
// land_features에 적재한다 (Phase 7-2). CP949, 한 행이 (필지 × 기준연도) 하나다.
// 컬럼(실측 2026-08-02):
//
//	0 고유번호(PNU) · 7 기준연도 · 17 토지이용상황 · 19 지형높이 · 21 지형형상
//	· 23 도로접면 · 25 데이터기준일자
//
// 번들엔 여러 날짜(연도별 스냅샷)가 섞여 있어 최신 날짜만 적재한다. land_use는
// 용도지역이 아니라 '토지이용상황'(원값 라벨) — 재분류 없이 그대로 적재. 지역선택
// 적재라 TRUNCATE 없이 upsert.
package landfeature

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	colPNU    = 0
	colYear   = 7
	colUse    = 17
	colHeight = 19
	colShape  = 21
	colRoad   = 23
	colDate   = 25
	colMin    = 26 // 0..25를 쓰므로 최소 26개 필드
)

var stgCols = []string{"pnu", "base_year", "land_use", "terrain_height", "terrain_shape", "road_side", "source_version"}

// Stats는 번들 적재 결과다.
type Stats struct {
	Rows    int
	Final   int
	Skipped int
}

// Load는 토지특성 번들(중첩 zip)에서 최신 날짜분 AL_D195 CSV를 land_features에 upsert한다.
func Load(ctx context.Context, pool *pgxpool.Pool, bundlePath string) (*Stats, error) {
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open bundle: %w", err)
	}
	defer zr.Close()

	latest := ""
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if strings.HasPrefix(base, "AL_D195_") {
			if d := dateOf(base); d > latest {
				latest = d
			}
		}
	}
	if latest == "" {
		return nil, fmt.Errorf("AL_D195_*_<date> 없음: %s", bundlePath)
	}

	if _, err := pool.Exec(ctx, `CREATE UNLOGGED TABLE IF NOT EXISTS lf_stg
		(pnu char(19), base_year smallint, land_use text, terrain_height text,
		 terrain_shape text, road_side text, source_version date)`); err != nil {
		return nil, fmt.Errorf("create stg: %w", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE lf_stg`); err != nil {
		return nil, err
	}

	stats := &Stats{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if !strings.HasPrefix(base, "AL_D195_") || dateOf(base) != latest {
			continue
		}
		// 번들 형태 두 가지: 중첩 zip(서울: AL_D195_*.zip→CSV) 또는 직접 CSV(경기).
		var err error
		switch strings.ToLower(filepath.Ext(base)) {
		case ".zip":
			err = loadNested(ctx, pool, f, stats)
		case ".csv":
			err = loadCSVEntry(ctx, pool, f, stats)
		}
		if err != nil {
			return stats, fmt.Errorf("%s: %w", base, err)
		}
	}
	if stats.Rows == 0 {
		return stats, fmt.Errorf("최신(%s) AL_D195 유효행 없음", latest)
	}

	tag, err := pool.Exec(ctx, `
		INSERT INTO land_features (pnu, base_year, land_use, terrain_height, terrain_shape, road_side, source_version)
		SELECT DISTINCT ON (pnu, base_year) pnu, base_year, land_use, terrain_height, terrain_shape, road_side, source_version
		FROM lf_stg ORDER BY pnu, base_year
		ON CONFLICT (pnu, base_year) DO UPDATE
		SET land_use = EXCLUDED.land_use, terrain_height = EXCLUDED.terrain_height,
		    terrain_shape = EXCLUDED.terrain_shape, road_side = EXCLUDED.road_side,
		    source_version = EXCLUDED.source_version`)
	if err != nil {
		return stats, fmt.Errorf("upsert: %w", err)
	}
	stats.Final = int(tag.RowsAffected())
	if _, err := pool.Exec(ctx, `DROP TABLE lf_stg`); err != nil {
		return stats, err
	}
	return stats, nil
}

// dateOf는 "AL_D195_11_20260519.(zip|csv)"에서 8자리 날짜를 뽑는다.
func dateOf(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name)) // .zip 또는 .csv
	i := strings.LastIndexByte(name, '_')
	if i < 0 {
		return ""
	}
	d := name[i+1:]
	if len(d) != 8 {
		return ""
	}
	return d
}

// loadNested는 중첩 zip을 임시 해제해 그 안의 CSV를 적재한다(서울 형태).
func loadNested(ctx context.Context, pool *pgxpool.Pool, f *zip.File, stats *Stats) error {
	tmp, err := writeTemp(f)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return err
	}
	defer zr.Close()
	var csvE *zip.File
	for _, e := range zr.File {
		if strings.HasSuffix(strings.ToLower(e.Name), ".csv") {
			csvE = e
			break
		}
	}
	if csvE == nil {
		return fmt.Errorf("no .csv in nested zip")
	}
	rc, err := csvE.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return loadCSVReader(ctx, pool, rc, stats)
}

// loadCSVEntry는 외부 zip 안의 CSV 엔트리를 직접 적재한다(경기 형태 — 중첩 없음).
func loadCSVEntry(ctx context.Context, pool *pgxpool.Pool, f *zip.File, stats *Stats) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return loadCSVReader(ctx, pool, rc, stats)
}

// loadCSVReader는 CP949 CSV 스트림을 파싱해 lf_stg에 CopyFrom한다.
func loadCSVReader(ctx context.Context, pool *pgxpool.Pool, r io.Reader, stats *Stats) error {
	cr := csv.NewReader(transform.NewReader(r, korean.EUCKR.NewDecoder()))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	chunk := make([][]any, 0, 50000)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if _, err := pool.CopyFrom(ctx, pgx.Identifier{"lf_stg"}, stgCols, pgx.CopyFromRows(chunk)); err != nil {
			return err
		}
		chunk = chunk[:0]
		return nil
	}
	first := true
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			stats.Skipped++
			continue
		}
		if first {
			first = false
			if len(rec) > colPNU && strings.TrimSpace(rec[colPNU]) == "고유번호" {
				continue // 헤더
			}
		}
		if len(rec) < colMin {
			stats.Skipped++
			continue
		}
		pnu := strings.TrimSpace(rec[colPNU])
		year, errY := strconv.Atoi(strings.TrimSpace(rec[colYear]))
		sv, okD := parseDate(strings.TrimSpace(rec[colDate]))
		if len(pnu) != 19 || errY != nil || !okD {
			stats.Skipped++
			continue
		}
		chunk = append(chunk, []any{
			pnu, int16(year),
			strings.TrimSpace(rec[colUse]), strings.TrimSpace(rec[colHeight]),
			strings.TrimSpace(rec[colShape]), strings.TrimSpace(rec[colRoad]), sv,
		})
		stats.Rows++
		if len(chunk) >= 50000 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// parseDate는 "2026-05-12" → time.Time. 실패 시 (_, false).
func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func writeTemp(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "landfeat-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, rc); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}
