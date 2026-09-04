// Package officialprice는 개별공시지가(NSDI AL_D151 CSV) → official_land_prices에
// 적재한다 (Phase 7-2). CP949, 한 행이 (필지 × 기준연도) 하나다. 컬럼(실측 2026-08-02):
//
//	0 고유번호(PNU 19자리) · 6 기준연도 · 8 공시지가(원/㎡) · 11 데이터기준일자
//
// 개별공시지가는 시군구/시도 선택 적재 정책이라 TRUNCATE 없이 upsert한다(다른 지역 보존).
package officialprice

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	colPNU   = 0
	colYear  = 6
	colPrice = 8
	colDate  = 11
	colMin   = 12 // 0..11을 쓰므로 최소 12개 필드
)

var stgCols = []string{"pnu", "base_year", "price_per_sqm", "source_version"}

// Stats는 적재 결과다.
type Stats struct {
	Rows    int // staging에 넣은 원행
	Final   int // upsert된 행
	Skipped int
}

// Load는 AL_D151 zip(단일 CSV)을 열어 official_land_prices에 upsert한다.
func Load(ctx context.Context, pool *pgxpool.Pool, zipPath string) (*Stats, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	var csvFile *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			csvFile = f
			break
		}
	}
	if csvFile == nil {
		return nil, fmt.Errorf("no .csv in %s", zipPath)
	}

	if _, err := pool.Exec(ctx, `CREATE UNLOGGED TABLE IF NOT EXISTS oip_stg
		(pnu char(19), base_year smallint, price_per_sqm bigint, source_version date)`); err != nil {
		return nil, fmt.Errorf("create stg: %w", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE oip_stg`); err != nil {
		return nil, err
	}

	rc, err := csvFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	cr := csv.NewReader(transform.NewReader(rc, korean.EUCKR.NewDecoder()))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	stats := &Stats{}
	chunk := make([][]any, 0, 50000)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if _, err := pool.CopyFrom(ctx, pgx.Identifier{"oip_stg"}, stgCols, pgx.CopyFromRows(chunk)); err != nil {
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
		price, errP := strconv.ParseInt(strings.TrimSpace(rec[colPrice]), 10, 64)
		sv, okD := parseDate(strings.TrimSpace(rec[colDate]))
		if len(pnu) != 19 || errY != nil || errP != nil || !okD {
			stats.Skipped++
			continue
		}
		chunk = append(chunk, []any{pnu, int16(year), price, sv})
		stats.Rows++
		if len(chunk) >= 50000 {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}

	// (pnu, base_year) 중복 제거하며 upsert. 지역선택 적재라 다른 지역/연도를 지우지 않는다.
	tag, err := pool.Exec(ctx, `
		INSERT INTO official_land_prices (pnu, base_year, price_per_sqm, source_version)
		SELECT DISTINCT ON (pnu, base_year) pnu, base_year, price_per_sqm, source_version
		FROM oip_stg ORDER BY pnu, base_year
		ON CONFLICT (pnu, base_year) DO UPDATE
		SET price_per_sqm = EXCLUDED.price_per_sqm, source_version = EXCLUDED.source_version`)
	if err != nil {
		return stats, fmt.Errorf("upsert: %w", err)
	}
	stats.Final = int(tag.RowsAffected())
	if _, err := pool.Exec(ctx, `DROP TABLE oip_stg`); err != nil {
		return stats, err
	}
	return stats, nil
}

// parseDate는 "2026-05-21" → time.Time. 실패 시 (_, false).
func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
