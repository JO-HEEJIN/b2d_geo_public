package juso

import (
	"archive/zip"
	"bufio"
	"context"
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
	Files     int   // 처리한 rnaddrkor 파일 수
	Staged    int64 // staging에 적재된 유효 행 수
	Skipped   int64 // 법정동코드(bcode) 형식 오류로 제외된 행 수
	Inserted  int64 // buildings에 최종 INSERT된 행 수 (중복 관리번호 제외)
	JibunRows int64 // 검증만 수행한 관련지번 행 수 (buildings 미반영, 아래 설명)
}

// buildings 적재는 도로명주소 한글 레코드만으로 충분하다. 한글 레코드는 자신의
// 대표 지번(법정리명/산여부/지번본번/지번부번)을 포함하므로 jibun_address를 그대로
// 만들 수 있다. 관련지번 파일은 한 주소에 여러 지번을 나열한 것이라 단일 컬럼에
// 담을 수 없어, 이 라운드에서는 레이아웃(14컬럼) 검증만 하고 병합하지 않는다.

var stagingColumns = []string{
	"road_address", "jibun_address", "building_name",
	"bcode", "norm_road", "norm_jibun", "juso_mgmt_no",
}

const createStagingSQL = `CREATE TEMP TABLE juso_staging (
	road_address  text,
	jibun_address text,
	building_name text,
	bcode         char(10),
	norm_road     text,
	norm_jibun    text,
	juso_mgmt_no  char(26)
) ON COMMIT DROP`

// staging -> buildings. geom/pnu는 NULL로 둔다(좌표는 위치정보DB 승인 후 채운다).
// 파일 내부 중복 관리번호는 DISTINCT ON으로 걸러낸다.
const insertFromStagingSQL = `INSERT INTO buildings
	(road_address, jibun_address, building_name, bcode, norm_road, norm_jibun, juso_mgmt_no, source, source_version)
SELECT DISTINCT ON (juso_mgmt_no)
	road_address,
	NULLIF(jibun_address, ''),
	NULLIF(building_name, ''),
	bcode,
	norm_road,
	NULLIF(norm_jibun, ''),
	juso_mgmt_no,
	$1,
	$2::date
FROM juso_staging
ORDER BY juso_mgmt_no
ON CONFLICT (juso_mgmt_no) WHERE juso_mgmt_no IS NOT NULL DO NOTHING`

// Load는 path(단일 txt, 디렉터리, 또는 전국 ZIP)의 도로명주소 한글 자료를
// buildings에 적재한다. 단일 트랜잭션에서 기존 source='juso' 행을 삭제한 뒤 다시
// 적재하므로 재실행 멱등이다. sourceVersion은 buildings.source_version(date)에
// 기록한다(예: "2026-05-01").
func Load(ctx context.Context, pool *pgxpool.Pool, path, sourceVersion string) (*LoadStats, error) {
	rnFiles, jbFiles, cleanup, err := resolveInputs(path)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(rnFiles) == 0 {
		return nil, fmt.Errorf("적재할 도로명주소 한글(rnaddrkor) 파일 없음: %s", path)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM buildings WHERE source = $1`, SourceName); err != nil {
		return nil, fmt.Errorf("기존 %s 행 삭제: %w", SourceName, err)
	}
	if _, err := tx.Exec(ctx, createStagingSQL); err != nil {
		return nil, fmt.Errorf("staging 생성: %w", err)
	}

	stats := &LoadStats{}
	for _, in := range rnFiles {
		if err := copyRnaddrkor(ctx, tx, in, stats); err != nil {
			return nil, fmt.Errorf("%s: %w", in.name, err)
		}
		stats.Files++
	}

	ct, err := tx.Exec(ctx, insertFromStagingSQL, SourceName, sourceVersion)
	if err != nil {
		return nil, fmt.Errorf("staging -> buildings: %w", err)
	}
	stats.Inserted = ct.RowsAffected()

	// 관련지번 파일은 레이아웃 검증만 한다(컬럼 수 불일치 시 적재 중단).
	for _, in := range jbFiles {
		if err := validateJibun(in, stats); err != nil {
			return nil, fmt.Errorf("%s: %w", in.name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return stats, nil
}

// copyRnaddrkor는 rnaddrkor 파일 하나를 열어 staging에 CopyFrom한다.
func copyRnaddrkor(ctx context.Context, tx pgx.Tx, in fileInput, stats *LoadStats) error {
	rc, err := in.open()
	if err != nil {
		return err
	}
	defer rc.Close()

	src := &buildingSource{sc: decodingScanner(rc), stats: stats}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"juso_staging"}, stagingColumns, src); err != nil {
		return fmt.Errorf("CopyFrom: %w", err)
	}
	return src.err
}

// validateJibun는 관련지번 파일을 파싱하며 14컬럼 레이아웃을 검증하고 행 수를 센다.
func validateJibun(in fileInput, stats *LoadStats) error {
	rc, err := in.open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return ParseJibun(rc, func(JibunRecord) error {
		stats.JibunRows++
		return nil
	})
}

// buildingSource는 pgx.CopyFromSource 구현이다. MS949 파일을 스트리밍하며 각 행을
// buildings 스테이징 값으로 변환한다. 컬럼 수 불일치는 즉시 중단하고, 법정동코드
// 형식 오류 행은 건너뛴다(스키마 검증).
type buildingSource struct {
	sc    *bufio.Scanner
	stats *LoadStats
	line  int
	cur   []any
	err   error
}

func (s *buildingSource) Next() bool {
	for s.sc.Scan() {
		s.line++
		text := strings.TrimRight(s.sc.Text(), "\r")
		if text == "" {
			continue
		}
		f, err := splitFields(text, RnaddrkorColumnCount)
		if err != nil {
			s.err = fmt.Errorf("도로명주소 한글 %d행: %w", s.line, err)
			return false
		}
		rec := rnaddrkorFromFields(f)
		if !validBcode(rec.LegalDongCode) {
			s.stats.Skipped++
			continue
		}
		road := rec.RoadAddress()
		jibun := rec.JibunAddress()
		s.cur = []any{
			road,
			jibun,
			rec.BuildingName(),
			rec.LegalDongCode,
			normalizePlaceholder(road),
			normalizePlaceholder(jibun),
			rec.MgmtNo,
		}
		s.stats.Staged++
		return true
	}
	s.err = s.sc.Err()
	return false
}

func (s *buildingSource) Values() ([]any, error) { return s.cur, nil }
func (s *buildingSource) Err() error             { return s.err }

// validBcode는 법정동코드가 10자리 숫자인지 검증한다.
func validBcode(s string) bool {
	if len(s) != 10 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// normalizePlaceholder는 Phase 1 임시 정규화다. 공백을 하나로 접고 앞뒤를 다듬는다.
// TODO(Phase 2): internal/address의 실제 주소 정규화(자모 분해 등)로 대체한다.
func normalizePlaceholder(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// fileInput은 지연 오픈 가능한 텍스트 소스 하나다.
type fileInput struct {
	name string
	open func() (io.ReadCloser, error)
}

// resolveInputs는 path를 rnaddrkor/jibun 입력 목록으로 분류해 해석한다. ZIP은 내부
// *.txt 엔트리를, 디렉터리는 *.txt 파일을, 그 외는 단일 파일로 취급한다. cleanup은
// ZIP 핸들 해제용이며 nil일 수 있다.
func resolveInputs(path string) (rnFiles, jbFiles []fileInput, cleanup func() error, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, nil, err
	}

	classify := func(name string, open func() (io.ReadCloser, error)) {
		in := fileInput{name: filepath.Base(name), open: open}
		switch fileKind(in.name) {
		case kindRnaddrkor:
			rnFiles = append(rnFiles, in)
		case kindJibun:
			jbFiles = append(jbFiles, in)
		}
	}

	switch {
	case info.IsDir():
		matches, err := filepath.Glob(filepath.Join(path, "*.txt"))
		if err != nil {
			return nil, nil, nil, err
		}
		for _, m := range matches {
			m := m
			classify(m, func() (io.ReadCloser, error) { return os.Open(m) })
		}
		return rnFiles, jbFiles, nil, nil

	case strings.HasSuffix(strings.ToLower(path), ".zip"):
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, fzip := range zr.File {
			if !strings.HasSuffix(strings.ToLower(fzip.Name), ".txt") {
				continue
			}
			fzip := fzip
			classify(fzip.Name, func() (io.ReadCloser, error) { return fzip.Open() })
		}
		return rnFiles, jbFiles, zr.Close, nil

	default:
		classify(path, func() (io.ReadCloser, error) { return os.Open(path) })
		return rnFiles, jbFiles, nil, nil
	}
}

type kind int

const (
	kindOther kind = iota
	kindRnaddrkor
	kindJibun
)

// fileKind는 파일명으로 rnaddrkor/관련지번을 구분한다. 전체분(rnaddrkor_지역명),
// 월변동(_mod), 일변동(TH_SGCO_RNADR_MST/LNBR) 명명을 모두 수용한다.
// 근거: docs/dataspec/juso.md §3.
func fileKind(name string) kind {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "jibun") || strings.Contains(n, "lnbr"):
		return kindJibun
	case strings.Contains(n, "rnaddrkor") || strings.Contains(n, "rnadr_mst") || strings.Contains(n, "rnadr"):
		return kindRnaddrkor
	default:
		return kindOther
	}
}
