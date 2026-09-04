// Package standardlots는 표준지공시지가 shapefile(NSDI AL_D152, 시도별 중첩 zip)을
// standard_lots에 적재한다 (Phase 7-1).
//
// 명세(실측 2026-07-28): 번들 zip 안에 시도별 AL_D152_<시도>_<날짜>.zip 이 있고,
// 각 nested zip이 shapefile 세트(EPSG:5186, DBF CP949, 폴리곤)다. 필드 인덱스 고정:
//
//	0=A0(PNU 19), 7=A7(기준연도), 9=A9(㎡당 공시지가), 13=A13(지목), 16=A16(용도지역),
//	18=A18(이용상황), 20=A20(형상), 22=A22(도로접면), 14=A14(면적), 23=A23(기준일 YYYYMMDD).
//
// 지세(고저)는 원자료에 없어 terrain_height는 NULL로 둔다.
//
// 원칙: 사실 저장만. geom은 필지 폴리곤의 ST_PointOnSurface(대표점, Point/4326). 계산·판단 없음.
package standardlots

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonas-p/go-shp"
	"golang.org/x/text/encoding/korean"
)

const upsertSQL = `
INSERT INTO standard_lots
  (pnu, base_year, price_per_sqm, land_category, use_zone, land_use, road_side,
   terrain_shape, area_sqm, geom, source, source_version)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
  ST_PointOnSurface(ST_MakeValid(ST_Transform(ST_SetSRID(ST_GeomFromText($10),5186),4326))),
  'molit_std',$11::date)
ON CONFLICT (pnu, base_year) DO UPDATE SET
  price_per_sqm=EXCLUDED.price_per_sqm, land_category=EXCLUDED.land_category,
  use_zone=EXCLUDED.use_zone, land_use=EXCLUDED.land_use, road_side=EXCLUDED.road_side,
  terrain_shape=EXCLUDED.terrain_shape, area_sqm=EXCLUDED.area_sqm, geom=EXCLUDED.geom,
  source_version=EXCLUDED.source_version`

// Stats는 번들 전체 적재 결과다.
type Stats struct {
	Files   int
	Rows    int
	Skipped int
}

// Load는 표준지공시지가 번들 zip을 열어 시도별 AL_D152 nested zip을 차례로 적재한다.
func Load(ctx context.Context, pool *pgxpool.Pool, bundlePath string) (*Stats, error) {
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open bundle: %w", err)
	}
	defer zr.Close()
	total := &Stats{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if !strings.HasPrefix(base, "AL_D152_") || !strings.HasSuffix(strings.ToLower(base), ".zip") {
			continue // D152(표준지)만; D153 등 무시
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
		return total, fmt.Errorf("번들에 AL_D152_*.zip 없음: %s", bundlePath)
	}
	return total, nil
}

// loadNested는 시도별 nested zip 하나(shapefile 세트)를 standard_lots에 upsert한다.
func loadNested(ctx context.Context, pool *pgxpool.Pool, f *zip.File) (*Stats, error) {
	tmpZip, err := writeTemp(f)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpZip)

	dir, err := os.MkdirTemp("", "stdlot-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	shpPath, err := extractShp(tmpZip, dir)
	if err != nil {
		return nil, err
	}
	r, err := shp.Open(shpPath)
	if err != nil {
		return nil, fmt.Errorf("open shp: %w", err)
	}
	defer r.Close()

	dec := korean.EUCKR.NewDecoder()
	stats := &Stats{}
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
		pnu := strings.TrimSpace(r.ReadAttribute(n, 0))
		year := atoiTrim(r.ReadAttribute(n, 7))
		price := atoiTrim(r.ReadAttribute(n, 9))
		srcVer := dateDashes(strings.TrimSpace(r.ReadAttribute(n, 23)))
		if len(pnu) != 19 || year == 0 || price <= 0 || srcVer == "" {
			stats.Skipped++ // NOT NULL 필드(pnu/base_year/price/source_version) 결측
			continue
		}
		wkt := polygonWKT(poly)
		if wkt == "" {
			stats.Skipped++
			continue
		}
		batch.Queue(upsertSQL, pnu, year, price,
			nz(dcz(dec, r.ReadAttribute(n, 13))), // land_category
			nz(dcz(dec, r.ReadAttribute(n, 16))), // use_zone
			nz(dcz(dec, r.ReadAttribute(n, 18))), // land_use
			nz(dcz(dec, r.ReadAttribute(n, 22))), // road_side
			nz(dcz(dec, r.ReadAttribute(n, 20))), // terrain_shape
			numOrNil(r.ReadAttribute(n, 14)),     // area_sqm (DBF 오버플로 "***"는 NULL)
			wkt, srcVer)
		stats.Rows++
		if batch.Len() >= 500 {
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

// writeTemp는 zip 안의 nested zip 엔트리를 임시 파일로 쓴다.
func writeTemp(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "stdlot-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, rc); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// extractShp는 nested zip에서 shp/dbf/shx를 풀고 .shp 경로를 돌려준다.
func extractShp(zipPath, dir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var shpPath string
	for _, f := range zr.File {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext != ".shp" && ext != ".dbf" && ext != ".shx" {
			continue
		}
		dst := filepath.Join(dir, "layer"+ext)
		if err := copyEntry(f, dst); err != nil {
			return "", err
		}
		if ext == ".shp" {
			shpPath = dst
		}
	}
	if shpPath == "" {
		return "", fmt.Errorf("no .shp in %s", zipPath)
	}
	return shpPath, nil
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

func atoiTrim(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// dcz는 CP949 바이트를 UTF-8로 디코드·트림한다(실패 시 원문 트림).
func dcz(dec interface{ String(string) (string, error) }, raw string) string {
	v, err := dec.String(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(v)
}

// nz는 빈 문자열을 NULL(nil)로 바꾼다(nullable 컬럼용).
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// numOrNil은 유효한 숫자면 원문(텍스트), 아니면 NULL을 돌려준다.
// DBF 폭 초과 시 "***…"로 표기되는데 이를 NULL로 처리한다.
func numOrNil(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return nil
	}
	return s
}

// dateDashes는 "20260515" → "2026-05-15"(8자리 아니면 "").
func dateDashes(s string) string {
	if len(s) != 8 {
		return ""
	}
	for i := 0; i < 8; i++ {
		if s[i] < '0' || s[i] > '9' {
			return ""
		}
	}
	return s[:4] + "-" + s[4:6] + "-" + s[6:8]
}

// polygonWKT는 shp 폴리곤을 MULTIPOLYGON WKT로 만든다.
// shapefile 규약: 시계방향(음의 부호면적) 링 = 외곽, 반시계 = 구멍.
func polygonWKT(p *shp.Polygon) string {
	type ring []shp.Point
	var polys [][]ring
	for i := range p.Parts {
		start := p.Parts[i]
		end := int32(len(p.Points))
		if i+1 < len(p.Parts) {
			end = p.Parts[i+1]
		}
		pts := p.Points[start:end]
		if len(pts) < 4 {
			continue
		}
		if signedArea2(pts) < 0 {
			polys = append(polys, []ring{pts})
		} else if len(polys) > 0 {
			polys[len(polys)-1] = append(polys[len(polys)-1], pts)
		} else {
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
