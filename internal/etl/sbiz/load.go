package sbiz

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LoadStats는 적재 결과 집계다.
type LoadStats struct {
	Files    int   // 처리한 CSV 파일 수
	Staged   int64 // staging에 적재된 유효 행 수
	Skipped  int64 // bbox/파싱/키 누락으로 제외된 행 수
	Inserted int64 // places에 최종 INSERT된 행 수 (중복 store_number 제외)
}

var stagingColumns = []string{
	"store_number", "name", "category_code",
	"jibun_address", "road_address", "norm_name",
	"lon", "lat",
}

const createStagingSQL = `CREATE TEMP TABLE sbiz_staging (
	store_number  text,
	name          text,
	category_code text,
	jibun_address text,
	road_address  text,
	norm_name     text,
	lon           double precision,
	lat           double precision
) ON COMMIT DROP`

// staging -> places. geom은 ST_MakePoint(경도, 위도) + SRID 4326으로 구성한다.
// 좌표 변환은 하지 않는다(원본이 이미 4326). 중복 store_number는 무시한다.
const insertFromStagingSQL = `INSERT INTO places
	(store_number, name, norm_name, category_code, road_address, jibun_address, geom, source, source_version)
SELECT
	store_number,
	name,
	norm_name,
	NULLIF(category_code, ''),
	NULLIF(road_address, ''),
	NULLIF(jibun_address, ''),
	ST_SetSRID(ST_MakePoint(lon, lat), 4326),
	$1,
	$2::date
FROM sbiz_staging
ON CONFLICT (store_number) DO NOTHING`

// Load는 path(단일 CSV, 디렉터리, 또는 전국 ZIP)의 상가정보를 places에 적재한다.
// 단일 트랜잭션에서 기존 source='sbiz' 행을 삭제한 뒤 다시 적재하므로 재실행 멱등이다.
// sourceVersion은 places.source_version(date)에 기록한다(예: "2026-03-31").
func Load(ctx context.Context, pool *pgxpool.Pool, path, sourceVersion string) (*LoadStats, error) {
	inputs, cleanup, err := resolveInputs(path)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("적재할 CSV 없음: %s", path)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM places WHERE source = $1`, SourceName); err != nil {
		return nil, fmt.Errorf("기존 %s 행 삭제: %w", SourceName, err)
	}
	if _, err := tx.Exec(ctx, createStagingSQL); err != nil {
		return nil, fmt.Errorf("staging 생성: %w", err)
	}

	stats := &LoadStats{}
	for _, in := range inputs {
		if err := copyOneFile(ctx, tx, in, stats); err != nil {
			return nil, fmt.Errorf("%s: %w", in.name, err)
		}
		stats.Files++
	}

	ct, err := tx.Exec(ctx, insertFromStagingSQL, SourceName, sourceVersion)
	if err != nil {
		return nil, fmt.Errorf("staging -> places: %w", err)
	}
	stats.Inserted = ct.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return stats, nil
}

// copyOneFile은 CSV 하나를 열어 헤더를 검증하고 유효 행을 staging에 CopyFrom한다.
func copyOneFile(ctx context.Context, tx pgx.Tx, in csvInput, stats *LoadStats) error {
	rc, err := in.open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(bufio.NewReaderSize(rc, 1<<20))
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("헤더 읽기: %w", err)
	}
	if err := validateHeader(header); err != nil {
		return err
	}
	r.FieldsPerRecord = ColumnCount

	src := &copySource{r: r, stats: stats}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"sbiz_staging"}, stagingColumns, src); err != nil {
		return fmt.Errorf("CopyFrom: %w", err)
	}
	return src.err
}

// validateHeader는 컬럼 수와 컬럼명이 명세와 일치하는지 확인한다. 불일치 시
// 적재를 중단한다(스키마 검증). 첫 컬럼의 UTF-8 BOM은 제거 후 비교한다.
func validateHeader(header []string) error {
	if len(header) != ColumnCount {
		return fmt.Errorf("컬럼 수 불일치: got %d want %d", len(header), ColumnCount)
	}
	for i, got := range header {
		got = strings.TrimSpace(got)
		if i == 0 {
			got = strings.TrimPrefix(got, "\ufeff")
		}
		if got != ColumnNames[i] {
			return fmt.Errorf("컬럼 %d 이름 불일치: got %q want %q", i+1, got, ColumnNames[i])
		}
	}
	return nil
}

// copySource는 pgx.CopyFromSource 구현이다. CSV를 스트리밍하며 유효 행만 흘려보낸다.
type copySource struct {
	r     *csv.Reader
	stats *LoadStats
	cur   stagedRow
	err   error
}

func (s *copySource) Next() bool {
	for {
		rec, err := s.r.Read()
		if err == io.EOF {
			return false
		}
		if err != nil {
			s.err = err
			return false
		}
		row, ok := parseRow(rec)
		if !ok {
			s.stats.Skipped++
			continue
		}
		s.cur = row
		s.stats.Staged++
		return true
	}
}

func (s *copySource) Values() ([]any, error) {
	r := s.cur
	var category any
	if r.category != "" {
		category = r.category
	}
	return []any{
		r.storeNumber, r.name, category,
		r.jibun, r.road, r.normName,
		r.lon, r.lat,
	}, nil
}

func (s *copySource) Err() error { return s.err }

// csvInput은 지연 오픈 가능한 CSV 소스 하나다.
type csvInput struct {
	name string
	open func() (io.ReadCloser, error)
}

// resolveInputs는 path를 CSV 입력 목록으로 해석한다. ZIP은 내부 *.csv 엔트리를,
// 디렉터리는 *.csv 파일을, 그 외는 단일 CSV로 취급한다. cleanup은 ZIP 핸들
// 해제용이며 nil일 수 있다.
func resolveInputs(path string) (inputs []csvInput, cleanup func() error, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}

	if info.IsDir() {
		matches, err := filepath.Glob(filepath.Join(path, "*.csv"))
		if err != nil {
			return nil, nil, err
		}
		for _, m := range matches {
			m := m
			inputs = append(inputs, csvInput{
				name: filepath.Base(m),
				open: func() (io.ReadCloser, error) { return os.Open(m) },
			})
		}
		return inputs, nil, nil
	}

	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range zr.File {
			if !strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
				continue
			}
			f := f
			inputs = append(inputs, csvInput{
				name: f.Name,
				open: func() (io.ReadCloser, error) { return f.Open() },
			})
		}
		return inputs, zr.Close, nil
	}

	inputs = append(inputs, csvInput{
		name: filepath.Base(path),
		open: func() (io.ReadCloser, error) { return os.Open(path) },
	})
	return inputs, nil, nil
}
