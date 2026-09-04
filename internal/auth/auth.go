package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ctxKey는 요청 컨텍스트에 인증 결과를 심기 위한 비공개 키 타입이다.
type ctxKey int

const (
	ctxKeyAPIKeyID ctxKey = iota
	ctxKeyRateRPS
)

// cacheTTL은 검증된 키 조회 결과의 기본 캐시 유효기간이다. 요청마다 DB를 때리지 않도록
// 두되, 이 기간만큼 revoke 반영이 지연됨(known tradeoff). 환경변수 B2D_AUTH_CACHE_TTL로
// 재정의 가능(온프레미스/고보안은 "0s"로 두어 캐시 없이 revoke 즉시 반영).
const cacheTTL = 30 * time.Second

// Authenticator는 X-API-Key(또는 Authorization: Bearer)를 SHA-256 해시로 대조하고
// scope를 검사하는 미들웨어를 제공한다.
type Authenticator struct {
	pool  *pgxpool.Pool
	mu    sync.Mutex
	cache map[string]cacheEntry // key: SHA-256 해시
	ttl   time.Duration         // 캐시 TTL (기본 cacheTTL, B2D_AUTH_CACHE_TTL로 재정의)
}

type cacheEntry struct {
	id         int64
	scopes     []string
	rateRPS    int
	monthlyCap int64 // 0 = 월 캡 없음
	expires    time.Time
}

// NewAuthenticator는 pool을 사용하는 인증기를 만든다.
func NewAuthenticator(pool *pgxpool.Pool) *Authenticator {
	return &Authenticator{pool: pool, cache: map[string]cacheEntry{}, ttl: cacheTTLFromEnv()}
}

// cacheTTLFromEnv는 B2D_AUTH_CACHE_TTL(예: "5s", "0s")을 읽어 캐시 TTL을 정한다.
// 미설정/파싱실패/음수면 기본값 cacheTTL. 0이면 캐시 무효(요청마다 DB 조회 → revoke 즉시).
func cacheTTLFromEnv() time.Duration {
	if v := os.Getenv("B2D_AUTH_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return cacheTTL
}

// APIKeyID는 인증 미들웨어가 심은 api_keys.id를 돌려준다. meter가 사용한다.
func APIKeyID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKeyAPIKeyID).(int64)
	return id, ok
}

// rateRPS는 인증된 키의 rate_limit_rps를 돌려준다(없으면 0).
func rateRPS(ctx context.Context) int {
	if v, ok := ctx.Value(ctxKeyRateRPS).(int); ok {
		return v
	}
	return 0
}

// Middleware는 인증·scope 검사 미들웨어다. /v1/health는 검사에서 제외한다.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		raw := extractKey(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "API key required")
			return
		}
		ent, ok, err := a.lookup(r.Context(), HashKey(raw))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "authentication failed")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
			return
		}
		if !hasScope(ent.scopes, requiredScope(r.URL.Path)) {
			writeError(w, http.StatusForbidden, "forbidden", "API key missing required scope")
			return
		}
		// /v1/usage는 캡 도달 후에도 열어 둔다: 셀프서브 고객이 잔여량·리셋 시점을
		// 확인하는 메타 엔드포인트라, 여기까지 막으면 429의 안내가 무의미해진다.
		if ent.monthlyCap > 0 && r.URL.Path != "/v1/usage" {
			used, err := a.monthlyUsage(r.Context(), ent.id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "usage check failed")
				return
			}
			if used >= ent.monthlyCap {
				writeError(w, http.StatusTooManyRequests, "monthly_cap_exceeded",
					"monthly call quota for this key is used up; resets next month (UTC). upgrade plans: https://api.birth2death.com/pricing.html")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKeyAPIKeyID, ent.id)
		ctx = context.WithValue(ctx, ctxKeyRateRPS, ent.rateRPS)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// lookup은 해시로 키를 조회한다. 캐시 우선, 미스 시 DB. 조회 실패(미존재)는 (_, false, nil).
func (a *Authenticator) lookup(ctx context.Context, hash string) (cacheEntry, bool, error) {
	now := time.Now()
	a.mu.Lock()
	if ent, ok := a.cache[hash]; ok && ent.expires.After(now) {
		a.mu.Unlock()
		return ent, true, nil
	}
	a.mu.Unlock()

	var ent cacheEntry
	err := a.pool.QueryRow(ctx,
		`SELECT id, scopes, rate_limit_rps, monthly_cap FROM api_keys
		 WHERE key_hash = $1 AND revoked_at IS NULL`, hash,
	).Scan(&ent.id, &ent.scopes, &ent.rateRPS, &ent.monthlyCap)
	if err == pgx.ErrNoRows {
		return cacheEntry{}, false, nil
	}
	if err != nil {
		return cacheEntry{}, false, err
	}
	ent.expires = now.Add(a.ttl)
	a.mu.Lock()
	a.cache[hash] = ent
	a.mu.Unlock()
	return ent, true, nil
}

// monthlyUsage는 이 달(UTC date_trunc 기준) 키의 총 호출 수를 usage_log에서 집계한다.
// 메타 엔드포인트 /v1/usage 호출은 캡 소모로 치지 않는다(잔여량 폴링이 쿼터를 깎으면 안 됨).
// 캡이 있는 키(monthly_cap > 0)에서만 요청마다 호출된다. (api_key_id, bucket) 인덱스를
// 타는 한 달 범위 SUM이라 셀프서브 rate 수준에서는 부담이 없고, meter가 비동기 flush
// (1초 주기)라 캡 초과 판정이 그만큼 늦을 수 있다(하드캡의 허용 오차).
func (a *Authenticator) monthlyUsage(ctx context.Context, keyID int64) (int64, error) {
	var used int64
	err := a.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(count), 0) FROM usage_log
		 WHERE api_key_id = $1 AND bucket >= date_trunc('month', now())
		   AND endpoint <> '/v1/usage'`, keyID,
	).Scan(&used)
	return used, err
}

// extractKey는 X-API-Key를 우선하고, 없으면 Authorization: Bearer에서 키를 뽑는다.
func extractKey(r *http.Request) string {
	if k := r.Header.Get("X-API-Key"); k != "" {
		return k
	}
	const bearer = "Bearer "
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, bearer) {
		return strings.TrimSpace(a[len(bearer):])
	}
	return ""
}

// requiredScope는 경로에 필요한 scope를 돌려준다. /v1/appraisal/*는 realestate,
// 나머지는 geo.
func requiredScope(path string) string {
	if strings.HasPrefix(path, "/v1/appraisal/") {
		return "realestate"
	}
	if path == "/v1/usage" {
		return "" // 사용량 조회는 인증만 하면 scope 무관(메타 엔드포인트)
	}
	return "geo"
}

func hasScope(scopes []string, want string) bool {
	if want == "" { // scope 불요 엔드포인트: 인증된 키면 통과
		return true
	}
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// writeError는 통일 에러 포맷 {"error":{"code","message"}}로 응답한다.
// 내부 정보(쿼리/스택)는 message에 절대 넣지 않는다.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
