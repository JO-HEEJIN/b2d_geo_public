// Package geocode는 주소 → 좌표 조회를 제공한다.
// 3단 폴백 (Phase 2-3): ① 파서 키 정확 매칭 (시도 생략 시 부분키+후보 검출)
// ② 지번 경로 (법정동 → parcels) ③ trigram 유사도.
// score는 similarity 원값 그대로 노출한다 (가공 점수 금지 원칙).
package geocode

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JO-HEEJIN/b2d_geo_public/internal/address"
)

// Result는 지오코딩 결과 1건이다.
type Result struct {
	RoadAddress string     `json:"road_address,omitempty"`
	JibunLabel  string     `json:"jibun_address,omitempty"` // 지번 경로 결과 표기
	Bcode       string     `json:"bcode,omitempty"`
	Point       [2]float64 `json:"point"` // [lon, lat]
	Match       string     `json:"match"` // exact | exact_partial | jibun | trigram | ambiguous
	Score       float64    `json:"score,omitempty"`
	Candidates  []string   `json:"candidates,omitempty"` // 복수 시도 히트 시
	Warnings    []string   `json:"warnings,omitempty"`
}

// ErrAmbiguous는 부분키가 복수 시도에 걸릴 때다 — Candidates에 후보 수록.
var ErrAmbiguous = errors.New("ambiguous: multiple sido candidates")

// Search는 자유 입력 주소를 3단 폴백으로 지오코딩한다.
func Search(ctx context.Context, pool *pgxpool.Pool, q string) (*Result, error) {
	p := address.Parse(q)

	// ① 도로명 정확 매칭
	if key, full := p.RoadKey(); key != "" {
		if full {
			if r, err := roadExact(ctx, pool, key); err == nil {
				r.Match = "exact"
				r.Warnings = p.Warnings
				return r, nil
			} else if err != pgx.ErrNoRows {
				return nil, err
			}
		} else {
			r, err := roadSuffix(ctx, pool, key)
			switch {
			case err == nil:
				r.Warnings = p.Warnings
				return r, nil
			case err == ErrAmbiguous:
				r.Warnings = p.Warnings
				return r, ErrAmbiguous
			case err != pgx.ErrNoRows:
				return nil, err
			}
		}
	}

	// ② 지번 경로 (법정동명 → bcode → parcels)
	if p.Jibun != "" && p.Emd != "" {
		if r, err := jibunPath(ctx, pool, p); err == nil {
			r.Warnings = p.Warnings
			return r, nil
		} else if err != pgx.ErrNoRows {
			return nil, err
		}
	}

	// ③ trigram 폴백 (도로명 주소 한정)
	cleaned := strings.Join(strings.Fields(q), " ")
	if r, err := trigram(ctx, pool, cleaned); err == nil {
		r.Warnings = p.Warnings
		return r, nil
	} else if err != pgx.ErrNoRows {
		return nil, err
	}
	return nil, pgx.ErrNoRows
}

func roadExact(ctx context.Context, pool *pgxpool.Pool, key string) (*Result, error) {
	var r Result
	err := pool.QueryRow(ctx, `
		SELECT road_address, bcode, ST_X(geom), ST_Y(geom)
		FROM buildings WHERE norm_road = $1 AND geom IS NOT NULL LIMIT 1`, key).
		Scan(&r.RoadAddress, &r.Bcode, &r.Point[0], &r.Point[1])
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// roadSuffix는 시도 생략 부분키를 접미 매칭한다. 서로 다른 주소 2곳 이상
// 히트면 ErrAmbiguous + 후보 반환 (단정 금지 원칙).
func roadSuffix(ctx context.Context, pool *pgxpool.Pool, partial string) (*Result, error) {
	// DISTINCT ON: 동일 주소의 복수 행(출입구 중복 등)을 1건으로 — 단일
	// 실주소를 ambiguous로 오판하지 않는다 (QA LOW-5)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (road_address) road_address, bcode, ST_X(geom), ST_Y(geom)
		FROM buildings
		WHERE norm_road LIKE '%' || $1 AND geom IS NOT NULL
		ORDER BY road_address LIMIT 5`, " "+partial)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.RoadAddress, &r.Bcode, &r.Point[0], &r.Point[1]); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(out) {
	case 0:
		return nil, pgx.ErrNoRows
	case 1:
		out[0].Match = "exact_partial"
		return &out[0], nil
	default:
		amb := &Result{Match: "ambiguous"}
		for _, c := range out {
			amb.Candidates = append(amb.Candidates, c.RoadAddress)
		}
		return amb, ErrAmbiguous
	}
}

// jibunPath는 법정동명(+시군구)으로 admin_areas에서 bcode를 찾고
// parcels의 대표점을 좌표로 쓴다. 산 지번은 PNU 11번째 자리(2)로 구분.
func jibunPath(ctx context.Context, pool *pgxpool.Pool, p address.Parsed) (*Result, error) {
	namePat := "%" + p.Emd
	if p.Sigungu != "" {
		namePat = "%" + p.Sigungu + " " + p.Emd
	}
	gb := "1"
	if p.San {
		gb = "2"
	}
	var r Result
	var emdName string
	err := pool.QueryRow(ctx, `
		SELECT a.name, pc.bcode,
		       ST_X(ST_PointOnSurface(pc.geom)), ST_Y(ST_PointOnSurface(pc.geom))
		FROM admin_areas a
		JOIN parcels pc ON pc.bcode = a.bcode
		WHERE a.name LIKE $1 AND pc.jibun = $2 AND substring(pc.pnu, 11, 1) = $3
		LIMIT 1`, namePat, p.Jibun, gb).
		Scan(&emdName, &r.Bcode, &r.Point[0], &r.Point[1])
	if err != nil {
		return nil, err
	}
	label := emdName + " "
	if p.San {
		label += "산"
	}
	r.JibunLabel = label + p.Jibun
	r.Match = "jibun"
	return &r, nil
}

// trigram은 norm_road 유사도 상위 1건을 돌려준다.
// tx는 트랜잭션 의미가 아니라 **커넥션 고정**용 — set_limit은 커넥션(세션)
// 단위라 풀에서 다른 커넥션으로 쿼리가 나가면 임계가 안 먹는다.
// set_limit(0.55): 기본 0.3은 긴 한국 주소에서 후보 100만 행을 만들어
// 112초가 걸렸다 (실측). 0.55에서 4초 + 오타 재현 유지.
func trigram(ctx context.Context, pool *pgxpool.Pool, q string) (*Result, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_limit(0.55)`); err != nil {
		return nil, err
	}
	var r Result
	err = tx.QueryRow(ctx, `
		SELECT road_address, bcode, ST_X(geom), ST_Y(geom), similarity(norm_road, $1)
		FROM buildings
		WHERE norm_road % $1 AND geom IS NOT NULL
		ORDER BY similarity(norm_road, $1) DESC LIMIT 1`, q).
		Scan(&r.RoadAddress, &r.Bcode, &r.Point[0], &r.Point[1], &r.Score)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.Match = "trigram"
	return &r, nil
}

// Exact는 v0 호환 API다 (정확 매칭 1단만).
func Exact(ctx context.Context, pool *pgxpool.Pool, q string) (*Result, error) {
	key := strings.Join(strings.Fields(q), " ")
	r, err := roadExact(ctx, pool, key)
	if err != nil {
		return nil, err
	}
	r.Match = "exact"
	return r, nil
}
