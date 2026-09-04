package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rate limit 파라미터.
//   - per-key 처리율은 api_keys.rate_limit_rps(키별 값)를 그대로 쓰고, burst는 그 2배.
//   - per-IP는 스키마에 파라미터가 없으므로 아래 상수를 쓴다.
//
// 지시서(6.5, 10절)가 수치를 지정하지 않아 team-lead 기본값(IP 20rps)을 상수화했다.
const (
	ipRatePerSec = 20.0 // IP별 기본 처리율(req/s)
	ipBurst      = 40.0 // IP별 버스트 상한(= 2 x rate)
	keyBurstMul  = 2.0  // per-key burst = rate_limit_rps x 2

	bucketIdleTTL = 30 * time.Minute // 이 시간 이상 미사용 버킷은 청소 대상
	reapInterval  = 10 * time.Minute // 청소 주기
)

// tokenBucket은 단일 주체(키 또는 IP)의 토큰 버킷 상태다.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// limiter는 주체 문자열별 토큰 버킷 집합이다.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

func newLimiter() *limiter {
	l := &limiter{buckets: map[string]*tokenBucket{}}
	go l.reapLoop()
	return l
}

// reapLoop는 주기적으로 오래 미사용된 버킷을 제거해 맵 무한 성장을 막는다.
// 프로세스 수명 동안 상주한다(서버당 limiter 2개).
func (l *limiter) reapLoop() {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for now := range t.C {
		l.reap(now)
	}
}

// reap은 now 기준 bucketIdleTTL 넘게 미사용된 버킷을 삭제한다. now를 인자로
// 받아 테스트에서 시간을 주입할 수 있게 한다.
func (l *limiter) reap(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if now.Sub(b.last) > bucketIdleTTL {
			delete(l.buckets, k)
		}
	}
}

// allow는 key에 대해 토큰 1개 소비를 시도한다. 실패 시 재시도까지의 대기시간을 돌려준다.
// now를 인자로 받아 테스트에서 시간을 주입할 수 있게 한다.
func (l *limiter) allow(key string, rate, burst float64, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	return false, wait
}

// RateLimiter는 키별·IP별 이중 토큰 버킷 미들웨어를 제공한다.
type RateLimiter struct {
	keyLim *limiter
	ipLim  *limiter
}

// NewRateLimiter는 빈 버킷 집합으로 초기화한다.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{keyLim: newLimiter(), ipLim: newLimiter()}
}

// IPMiddleware는 IP별 제한만 적용한다. 인증 **앞단**(최외곽)에 배선해
// 미인증·무효키 플러드도 IP 단위로 막는다 (QA MED-1: auth 앞 무제한
// DB 조회 증폭 차단). /v1/health는 제외.
func (rl *RateLimiter) IPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		if ok, wait := rl.ipLim.allow("ip:"+clientIP(r), ipRatePerSec, ipBurst, time.Now()); !ok {
			tooMany(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Middleware는 키별 제한을 적용한다. 인증 미들웨어 뒤에 배선되어야
// 컨텍스트의 키 정보를 읽는다. (IP 제한은 IPMiddleware가 앞단에서 수행)
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		now := time.Now()
		id, _ := APIKeyID(r.Context())
		rps := float64(rateRPS(r.Context()))
		if rps <= 0 {
			rps = ipRatePerSec // 방어적 기본값(정상 경로에선 인증이 rps를 심는다)
		}
		if ok, wait := rl.keyLim.allow("key:"+strconv.FormatInt(id, 10), rps, rps*keyBurstMul, now); !ok {
			tooMany(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP는 RemoteAddr의 호스트부만 쓴다. X-Forwarded-For는 위조 가능하므로
// 신뢰 프록시 화이트리스트가 없는 현 단계에서는 신뢰하지 않는다.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
}
