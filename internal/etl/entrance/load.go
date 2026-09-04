// Package entrance는 도로명주소 출입구 전체분(txt, CP949, 19컬럼)을
// buildings 테이블에 적재한다. 명세: docs/dataspec/juso_entrance.md.
// 좌표는 EPSG:5179 → 4326 변환. 같은 도로명주소(지하·본번·부번 동일)의
// 복수 출입구는 첫 행만 적재한다 (건물 단위 대표 좌표).
package entrance

import (
	"archive/zip"
	"bufio"
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/encoding/korean"
)

// LoadStats는 zip 1개 적재 결과다.
type LoadStats struct {
	Rows    int // 적재 행
	Dup     int // 동일 주소 중복 출입구 (스킵)
	Skipped int // 필드 이상 (스킵)
}

const upsertSQL = `
INSERT INTO buildings (road_address, jibun_address, building_name, bcode, pnu,
    geom, norm_road, norm_jibun, source, source_version)
VALUES ($1, NULL, NULL, $2, NULL,
    ST_Transform(ST_SetSRID(ST_MakePoint($3, $4), 5179), 4326),
    $5, NULL, 'juso_entrance', $6::date)`

// Norm은 도로명주소 정확 매칭 키를 만든다.
// 기존 juso 한글DB 적재분과 동일 관례: 어절 단일 공백 결합
// ("서울특별시 중구 세종대로 110"). Phase 2 파서가 질의를 같은 형태로
// 정규화하면 정확 매칭 1단이 성립한다.
func Norm(sido, sigungu, road, under, bon, bu string) string {
	num := bon
	if bu != "" && bu != "0" {
		num += "-" + bu
	}
	parts := []string{sido, sigungu, road}
	if under == "1" {
		parts = append(parts, "지하")
	}
	parts = append(parts, num)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// Load는 출입구 시도 zip 1개를 buildings에 적재한다.
func Load(ctx context.Context, pool *pgxpool.Pool, zipPath, sourceVersion string) (*LoadStats, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	dec := korean.EUCKR.NewDecoder()
	stats := &LoadStats{}
	seen := make(map[string]struct{}, 1<<20)
	batch := &pgx.Batch{}
	flush := func() error {
		if batch.Len() == 0 {
			return nil
		}
		br := pool.SendBatch(ctx, batch)
		defer br.Close()
		for i := 0; i < batch.Len(); i++ {
			if _, err := br.Exec(); err != nil {
				return err
			}
		}
		batch = &pgx.Batch{}
		return nil
	}

	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".txt") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line, err := dec.Bytes(sc.Bytes())
			if err != nil {
				stats.Skipped++
				continue
			}
			p := strings.Split(string(line), "|")
			if len(p) != 19 {
				stats.Skipped++
				continue
			}
			bcode, sido, sigungu := p[1], p[2], p[3]
			road, under, bon, bu := p[7], p[8], p[9], p[10]
			x, y := strings.TrimSpace(p[17]), strings.TrimSpace(p[18])
			if len(bcode) != 10 || road == "" || bon == "" || x == "" || y == "" {
				stats.Skipped++
				continue
			}
			key := Norm(sido, sigungu, road, under, bon, bu)
			if _, ok := seen[key]; ok {
				stats.Dup++
				continue
			}
			seen[key] = struct{}{}

			addr := sido + " " + sigungu + " " + road + " "
			if under == "1" {
				addr += "지하 "
			}
			addr += bon
			if bu != "" && bu != "0" {
				addr += "-" + bu
			}
			batch.Queue(upsertSQL, addr, bcode, x, y, key, sourceVersion)
			stats.Rows++
			if batch.Len() >= 1000 {
				if err := flush(); err != nil {
					rc.Close()
					return stats, err
				}
			}
		}
		rc.Close()
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}
