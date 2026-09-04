// Package parcel은 parcels 테이블 조회를 제공한다 (사실 반환만).
package parcel

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Parcel은 필지 1건의 조회 결과다.
type Parcel struct {
	PNU           string          `json:"pnu"`
	Bcode         string          `json:"bcode"`
	AddressName   string          `json:"address_name,omitempty"` // 법정동 전체명
	Jibun         string          `json:"jibun"`
	LandCategory  string          `json:"land_category,omitempty"`
	SourceVersion string          `json:"source_version"`
	Centroid      [2]float64      `json:"centroid"` // [lon, lat]
	Geometry      json.RawMessage `json:"geometry,omitempty"`
}

const baseSelect = `
SELECT p.pnu, p.bcode, COALESCE(a.name,''), p.jibun, COALESCE(p.land_category,''),
       p.source_version::text,
       ST_X(ST_PointOnSurface(p.geom)), ST_Y(ST_PointOnSurface(p.geom)),
       ST_AsGeoJSON(p.geom, 6)
FROM parcels p LEFT JOIN admin_areas a ON a.bcode = p.bcode `

func scanOne(row pgx.Row, withGeom bool) (*Parcel, error) {
	var p Parcel
	var geo string
	if err := row.Scan(&p.PNU, &p.Bcode, &p.AddressName, &p.Jibun, &p.LandCategory,
		&p.SourceVersion, &p.Centroid[0], &p.Centroid[1], &geo); err != nil {
		return nil, err
	}
	if withGeom {
		p.Geometry = json.RawMessage(geo)
	}
	return &p, nil
}

// ByPNU는 PNU(19자리)로 필지를 찾는다.
func ByPNU(ctx context.Context, pool *pgxpool.Pool, pnu string, withGeom bool) (*Parcel, error) {
	return scanOne(pool.QueryRow(ctx, baseSelect+`WHERE p.pnu = $1`, pnu), withGeom)
}

// ByPoint는 WGS84 좌표를 포함하는 필지를 찾는다.
func ByPoint(ctx context.Context, pool *pgxpool.Pool, lon, lat float64, withGeom bool) (*Parcel, error) {
	return scanOne(pool.QueryRow(ctx,
		baseSelect+`WHERE ST_Contains(p.geom, ST_SetSRID(ST_MakePoint($1,$2),4326)) LIMIT 1`,
		lon, lat), withGeom)
}

// ByJibun은 법정동코드 + 지번(예: "179-3")으로 필지를 찾는다.
// 같은 지번이 토지/임야 대장에 공존할 수 있어 (PNU 11번째 자리 1=토지, 2=임야)
// san=false면 토지, san=true면 임야를 조회한다.
func ByJibun(ctx context.Context, pool *pgxpool.Pool, bcode, jibun string, san, withGeom bool) (*Parcel, error) {
	gb := "1"
	if san {
		gb = "2"
	}
	return scanOne(pool.QueryRow(ctx,
		baseSelect+`WHERE p.bcode = $1 AND p.jibun = $2 AND substring(p.pnu, 11, 1) = $3 LIMIT 1`,
		bcode, jibun, gb), withGeom)
}
