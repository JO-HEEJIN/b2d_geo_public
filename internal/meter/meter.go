package meter

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JO-HEEJIN/b2d_geo_public/internal/auth"
)

const (
	meterBuffer   = 4096            // 채널 버퍼. 넘치면 이벤트를 버려 요청 경로를 막지 않는다.
	flushMaxItems = 100             // 이 건수 이상 쌓이면 즉시 flush
	flushInterval = 1 * time.Second // 또는 이 주기마다 flush
)

// event는 한 요청의 계측 단위다.
type event struct {
	keyID    int64
	endpoint string
	ts       time.Time
}

// aggKey는 flush 시 (키, 엔드포인트, 분 버킷)로 집계하는 키다.
type aggKey struct {
	keyID    int64
	endpoint string
	bucket   time.Time
}

// Meter는 요청 경로를 막지 않는 사용량 계측기다. 미들웨어가 채널에 이벤트를 넣고,
// 백그라운드 goroutine이 배치로 usage_log에 적재한다.
type Meter struct {
	pool    *pgxpool.Pool
	ch      chan event
	dropped atomic.Int64  // 채널 포화로 버린 이벤트 수(관측용)
	done    chan struct{} // run goroutine 종료 신호
}

// New는 계측기를 만들고 flush goroutine을 시작한다.
func New(pool *pgxpool.Pool) *Meter {
	m := &Meter{pool: pool, ch: make(chan event, meterBuffer), done: make(chan struct{})}
	go m.run()
	return m
}

// Close는 이벤트 채널을 닫아 잔여 이벤트를 마지막으로 적재하고 run goroutine이
// 끝날 때까지 기다린다. 호출 전에 HTTP 서버를 먼저 Shutdown 해 Middleware의
// 채널 송신이 끝나야 한다(닫힌 채널 송신 panic 방지).
func (m *Meter) Close() {
	close(m.ch)
	<-m.done
}

// Middleware는 인증된 요청을 논블로킹으로 계측한다. 인증 미들웨어 뒤에 배선한다.
func (m *Meter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.APIKeyID(r.Context()); ok {
			e := event{keyID: id, endpoint: normalizeEndpoint(r.URL.Path), ts: time.Now()}
			select {
			case m.ch <- e:
			default:
				m.dropped.Add(1) // 요청 경로를 절대 막지 않는다: 포화 시 버린다.
			}
		}
		next.ServeHTTP(w, r)
	})
}

// run은 flushMaxItems 또는 flushInterval 조건으로 usage_log에 배치 적재한다.
func (m *Meter) run() {
	defer close(m.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	agg := map[aggKey]int{}
	n := 0
	flush := func() {
		if len(agg) == 0 {
			return
		}
		m.insert(agg)
		agg = map[aggKey]int{}
		n = 0
	}
	for {
		select {
		case e, ok := <-m.ch:
			if !ok {
				flush() // 채널이 닫혔고 버퍼도 비었다: 잔여 flush 후 종료
				return
			}
			agg[aggKey{keyID: e.keyID, endpoint: e.endpoint, bucket: e.ts.Truncate(time.Minute)}]++
			n++
			if n >= flushMaxItems {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// insert는 집계 결과를 usage_log에 COPY로 적재한다. 실패는 로그만 남기고 계속한다
// (계측 실패가 서비스에 영향을 주지 않도록).
func (m *Meter) insert(agg map[aggKey]int) {
	rows := make([][]any, 0, len(agg))
	for k, c := range agg {
		rows = append(rows, []any{k.keyID, k.endpoint, c, k.bucket})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.pool.CopyFrom(ctx, pgx.Identifier{"usage_log"},
		[]string{"api_key_id", "endpoint", "count", "bucket"}, pgx.CopyFromRows(rows))
	if err != nil {
		log.Printf("meter: usage_log insert failed: %v", err)
	}
}

// normalizeEndpoint는 가변 경로 세그먼트를 템플릿으로 접어 endpoint 카디널리티를 낮춘다.
// 현재 가변 경로는 /v1/parcel/{pnu} 하나뿐이다.
func normalizeEndpoint(path string) string {
	const p = "/v1/parcel/"
	if strings.HasPrefix(path, p) {
		rest := path[len(p):]
		if rest != "" && rest != "by-point" && rest != "by-jibun" && !strings.Contains(rest, "/") {
			return "/v1/parcel/{pnu}"
		}
	}
	return path
}
