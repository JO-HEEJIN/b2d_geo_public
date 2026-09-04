package sbiz

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JO-HEEJIN/b2d_geo_public/internal/address"
)

// BackfillChoseong은 places.choseong을 norm_name의 초성으로 채운다 (멱등:
// NULL 행만). Phase 3 초성 검색("ㅇㄴㄹㅇㄱ" → 온누리약국)의 데이터 준비.
func BackfillChoseong(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var total int64
	for {
		rows, err := pool.Query(ctx, `
			SELECT id, norm_name FROM places
			WHERE choseong IS NULL ORDER BY id LIMIT 20000`)
		if err != nil {
			return total, err
		}
		var ids []int64
		var chos []string
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return total, err
			}
			ids = append(ids, id)
			chos = append(chos, address.Choseong(name))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		tag, err := pool.Exec(ctx, `
			UPDATE places p SET choseong = u.c
			FROM unnest($1::bigint[], $2::text[]) AS u(id, c)
			WHERE p.id = u.id`, ids, chos)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		fmt.Printf("choseong backfill: %d rows\n", total)
	}
}
