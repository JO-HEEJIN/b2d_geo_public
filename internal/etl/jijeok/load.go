// Package jijeok는 V-World 연속지적도형정보(시도별 shapefile zip)를 parcels
// 테이블에 적재한다. 명세: docs/dataspec/jijeok.md — EPSG:5186, DBF는 CP949
// (.cpg의 UTF-8 표기는 실측상 거짓), PNU=A1(19자리), 지목은 A5의 한글 접미.
package jijeok

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonas-p/go-shp"
	"golang.org/x/text/encoding/korean"
)

// LoadStats는 zip 1개 적재 결과다.
type LoadStats struct {
	Rows    int // upsert된 필지 수
	Skipped int // PNU 길이 이상 등으로 건너뛴 행
}

const upsertSQL = `
INSERT INTO parcels (pnu, bcode, jibun, geom, land_category, source, source_version)
VALUES ($1, $2, $3,
        ST_Multi(ST_CollectionExtract(ST_MakeValid(
            ST_Transform(ST_SetSRID(ST_GeomFromText($4), 5186), 4326)), 3)),
        $5, 'vworld_jijeok', $6::date)
ON CONFLICT (pnu) DO UPDATE SET
    bcode = EXCLUDED.bcode, jibun = EXCLUDED.jibun, geom = EXCLUDED.geom,
    land_category = EXCLUDED.land_category, source_version = EXCLUDED.source_version`

// Load는 연속지적도 시도 zip 1개를 임시 해제 후 parcels에 upsert한다.
// 대용량 시도는 V-World가 100만 레코드 단위로 shapefile을 여러 벌 담으므로
// zip 안의 모든 shapefile을 적재한다. sourceVersion은 파일명의 기준일을 넘긴다.
func Load(ctx context.Context, pool *pgxpool.Pool, zipPath, sourceVersion string) (*LoadStats, error) {
	dir, err := os.MkdirTemp("", "jijeok-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	shpPaths, err := extract(zipPath, dir)
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", zipPath, err)
	}

	stats := &LoadStats{}
	for _, shpPath := range shpPaths {
		if err := loadShapefile(ctx, pool, shpPath, sourceVersion, stats); err != nil {
			return stats, fmt.Errorf("%s: %w", filepath.Base(shpPath), err)
		}
	}
	return stats, nil
}

// loadShapefile는 shapefile 하나를 읽어 parcels에 upsert한다(stats에 누적).
func loadShapefile(ctx context.Context, pool *pgxpool.Pool, shpPath, sourceVersion string, stats *LoadStats) error {
	r, err := shp.Open(shpPath)
	if err != nil {
		return fmt.Errorf("open shp: %w", err)
	}
	defer r.Close()

	// 필드 인덱스는 명세 고정: 0=A0,1=A1(PNU),2=A2(bcode),3=A3,4=A4(지번),5=A5(지번+지목)
	dec := korean.EUCKR.NewDecoder()
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

	for r.Next() {
		n, s := r.Shape()
		poly, ok := s.(*shp.Polygon)
		if !ok {
			stats.Skipped++
			continue
		}
		pnu := strings.TrimSpace(r.ReadAttribute(n, 1))
		bcode := strings.TrimSpace(r.ReadAttribute(n, 2))
		jibun := strings.TrimSpace(r.ReadAttribute(n, 4))
		rawA5 := r.ReadAttribute(n, 5)
		if len(pnu) != 19 || len(bcode) != 10 {
			stats.Skipped++
			continue
		}
		a5, err := dec.String(rawA5)
		if err != nil {
			a5 = ""
		}
		wkt := polygonWKT(poly)
		if wkt == "" {
			stats.Skipped++
			continue
		}
		batch.Queue(upsertSQL, pnu, bcode, jibun, wkt, landCategory(strings.TrimSpace(a5)), sourceVersion)
		stats.Rows++
		if batch.Len() >= 500 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// extract는 zip 안의 모든 shp/dbf/shx를 원래 basename으로 풀고 .shp 경로 전부를
// 돌려준다. basename을 보존해야 go-shp가 각 .shp의 짝 .dbf/.shx를 찾는다.
func extract(zipPath, dir string) ([]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var shpPaths []string
	for _, f := range zr.File {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext != ".shp" && ext != ".dbf" && ext != ".shx" {
			continue
		}
		dst := filepath.Join(dir, filepath.Base(f.Name))
		if err := copyEntry(f, dst); err != nil {
			return nil, err
		}
		if ext == ".shp" {
			shpPaths = append(shpPaths, dst)
		}
	}
	if len(shpPaths) == 0 {
		return nil, fmt.Errorf("no .shp in %s", zipPath)
	}
	return shpPaths, nil
}

func copyEntry(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, rc)
	return err
}

// polygonWKT는 shp 폴리곤을 MULTIPOLYGON WKT로 만든다.
// shapefile 규약: 시계방향(음의 부호면적) 링 = 외곽, 반시계 = 구멍.
func polygonWKT(p *shp.Polygon) string {
	type ring []shp.Point
	var polys [][]ring // 각 폴리곤 = [외곽, 구멍...]
	for i := range p.Parts {
		start := p.Parts[i]
		end := int32(len(p.Points))
		if i+1 < len(p.Parts) {
			end = p.Parts[i+1]
		}
		pts := p.Points[start:end]
		if len(pts) < 4 { // 닫힌 링 최소 4점
			continue
		}
		if signedArea2(pts) < 0 { // 외곽
			polys = append(polys, []ring{pts})
		} else if len(polys) > 0 { // 직전 폴리곤의 구멍
			polys[len(polys)-1] = append(polys[len(polys)-1], pts)
		} else { // 구멍이 먼저 오는 비정상 — 외곽으로 취급
			polys = append(polys, []ring{pts})
		}
	}
	if len(polys) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MULTIPOLYGON(")
	for pi, rings := range polys {
		if pi > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		for ri, rg := range rings {
			if ri > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('(')
			for qi, pt := range rg {
				if qi > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, "%.3f %.3f", pt.X, pt.Y)
			}
			b.WriteByte(')')
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return b.String()
}

func signedArea2(pts []shp.Point) float64 {
	var a float64
	for i := 0; i < len(pts)-1; i++ {
		a += pts[i].X*pts[i+1].Y - pts[i+1].X*pts[i].Y
	}
	return a
}

// landCategory는 A5("179-3대")에서 지번 뒤 한글 지목 접미를 뽑는다.
func landCategory(a5 string) string {
	runes := []rune(a5)
	i := len(runes)
	for i > 0 && runes[i-1] >= '가' && runes[i-1] <= '힣' {
		i--
	}
	return string(runes[i:])
}
