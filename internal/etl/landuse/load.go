// Package landuse는 토지이용계획정보(NSDI AL_D155 CSV, 시도별×날짜별 중첩 zip)를
// land_use_plan에 적재한다 (Phase 7-2).
//
// AL_D155 CSV는 한 행이 (필지 × 용도지역/지구/구역) 하나다 — SHP(AL_D154)의 콤마-패킹·
// 254자 truncation 문제가 없어 명칭이 온전하다. 컬럼(실측 2026-07-29):
//
//	0 고유번호(PNU) · 8 저촉여부(포함/저촉/접함) · 9 용도지역지구코드 · 10 용도지역지구명
//	· 12 데이터기준일자(YYYY-MM-DD). 인코딩 CP949, 따옴표로 콤마 필드 감쌈.
//
// 번들엔 여러 날짜(월 스냅샷)가 섞여 있어 최신 날짜만 적재한다.
// 적재: TRUNCATE 후 CSV→UNLOGGED staging에 CopyFrom → DISTINCT ON으로 (pnu,zone_code)
// 중복 제거하며 land_use_plan에 INSERT. 원칙: 사실 저장만(원자료 포함/저촉/접함 그대로).
package landuse

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	colPNU  = 0
	colRel  = 8
	colCode = 9
	colName = 10
	colDate = 12
	colMin  = 13 // 0..12를 쓰므로 최소 13개 필드
)

var stgCols = []string{"pnu", "zone_code", "zone_name", "relation", "source_version"}

// Stats는 번들 전체 적재 결과다.
type Stats struct {
	Files   int
	Rows    int // staging에 넣은 원행(중복 제거 전)
	Final   int // land_use_plan 최종 행(중복 제거 후)
	Skipped int
}

// Load는 land_use_plan을 비우고 AL_D155 CSV 중 최신 날짜분을 적재한다.
func Load(ctx context.Context, pool *pgxpool.Pool, bundlePath string) (*Stats, error) {
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open bundle: %w", err)
	}
	defer zr.Close()

	latest := ""
	for _, f := range zr.File {
		if d := dateOf(filepath.Base(f.Name)); d > latest {
			latest = d
		}
	}
	if latest == "" {
		return nil, fmt.Errorf("AL_D155_*_<date>.zip 없음: %s", bundlePath)
	}

	if _, err := pool.Exec(ctx, `TRUNCATE land_use_plan`); err != nil {
		return nil, fmt.Errorf("truncate: %w", err)
	}
	if _, err := pool.Exec(ctx, `CREATE UNLOGGED TABLE IF NOT EXISTS lu_stg
		(pnu char(19), zone_code text, zone_name text, relation text, source_version date)`); err != nil {
		return nil, fmt.Errorf("create stg: %w", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE lu_stg`); err != nil {
		return nil, err
	}

	total := &Stats{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if !strings.HasPrefix(base, "AL_D155_") || dateOf(base) != latest {
			continue
		}
		st, err := loadNested(ctx, pool, f)
		if err != nil {
			return total, fmt.Errorf("%s: %w", base, err)
		}
		total.Files++
		total.Rows += st.Rows
		total.Skipped += st.Skipped
		fmt.Fprintf(os.Stderr, "  %s: rows=%d skipped=%d\n", base, st.Rows, st.Skipped)
	}
	if total.Files == 0 {
		return total, fmt.Errorf("최신(%s) AL_D155 없음", latest)
	}

	// (pnu, zone_code) 중복 제거하며 최종 테이블로. 수억 행이라 한 세션에 work_mem를
	// 컨테이너 메모리 한도(≈1.9GB) 안에서 384MB로 올리고(2GB는 OOM 크래시 유발 — 실측),
	// PK를 일시 제거해 정렬·인덱스 비용을 낮춘다. 전용 커넥션이라야 SET이 이후 문에 적용된다.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return total, err
	}
	defer conn.Release()
	for _, stmt := range []string{
		"SET work_mem='384MB'",
		"SET maintenance_work_mem='384MB'",
		"ALTER TABLE land_use_plan DROP CONSTRAINT IF EXISTS land_use_plan_pkey",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return total, fmt.Errorf("dedup prep: %w", err)
		}
	}
	tag, err := conn.Exec(ctx, `
		INSERT INTO land_use_plan (pnu, zone_code, zone_name, relation, source_version)
		SELECT DISTINCT ON (pnu, zone_code) pnu, zone_code, zone_name, relation, source_version
		FROM lu_stg ORDER BY pnu, zone_code`)
	if err != nil {
		return total, fmt.Errorf("dedup insert: %w", err)
	}
	total.Final = int(tag.RowsAffected())
	if _, err := conn.Exec(ctx, `ALTER TABLE land_use_plan ADD PRIMARY KEY (pnu, zone_code)`); err != nil {
		return total, fmt.Errorf("re-add pk: %w", err)
	}
	if _, err := conn.Exec(ctx, `DROP TABLE lu_stg`); err != nil {
		return total, err
	}
	return total, nil
}

// dateOf는 "AL_D155_36_20260711(.zip)"에서 8자리 날짜를 뽑는다.
func dateOf(name string) string {
	name = strings.TrimSuffix(name, ".zip")
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

func loadNested(ctx context.Context, pool *pgxpool.Pool, f *zip.File) (*Stats, error) {
	tmpZip, err := writeTemp(f)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpZip)
	zr, err := zip.OpenReader(tmpZip)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("no .csv in nested zip")
	}
	rc, err := csvE.Open()
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
		if _, err := pool.CopyFrom(ctx, pgx.Identifier{"lu_stg"}, stgCols, pgx.CopyFromRows(chunk)); err != nil {
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
		code := strings.TrimSpace(rec[colCode])
		sv, ok := parseDate(strings.TrimSpace(rec[colDate]))
		if len(pnu) != 19 || code == "" || !ok {
			stats.Skipped++
			continue
		}
		row := []any{pnu, code, strings.TrimSpace(rec[colName]), strings.TrimSpace(rec[colRel]), sv}
		chunk = append(chunk, row)
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
	return stats, nil
}

// parseDate는 "2026-07-08" → time.Time. 실패 시 (_, false).
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
	tmp, err := os.CreateTemp("", "landuse-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, rc); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}
