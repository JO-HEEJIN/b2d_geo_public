// 요청 ID 미들웨어: 모든 /v1 요청에 X-Request-Id 응답 헤더를 부여해 브라우저
// 콘솔 에러와 서버 로그를 상호 대조할 수 있게 한다. 프론트(shared.js)는 실패 시
// 이 ID를 콘솔과 화면에 함께 남기고, 서버는 에러 응답(>=400)만 ID·상태·소요시간과
// 함께 로그한다 (정상 트래픽 소음 방지; 사용량 집계는 meter가 담당).
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

type reqIDKey struct{}

// RequestID는 미들웨어가 심은 요청 ID를 돌려준다 (없으면 "").
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(reqIDKey{}).(string)
	return id
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [6]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		w.Header().Set("X-Request-Id", id)
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		// panic 복구: 기존(net/http 기본)에는 연결만 끊겼는데, 이제 스택을 로그로
		// 남기고 Sentry에 보고한 뒤 (아직 응답 전이면) 500을 돌려준다. 서버 프로세스는
		// 계속 산다. ErrAbortHandler는 의도된 중단 신호라 그대로 다시 던진다.
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			if !sw.wrote {
				writeErr(sw, http.StatusInternalServerError, "internal error")
			}
			log.Printf("req %s: PANIC %s %s: %v\n%s",
				id, r.Method, r.URL.RequestURI(), rec, debug.Stack())
			capturePanic(r, id, sw.status, rec)
		}()
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), reqIDKey{}, id)))
		if sw.status >= 400 {
			log.Printf("req %s: %s %s -> %d (%s)",
				id, r.Method, r.URL.RequestURI(), sw.status,
				time.Since(start).Round(time.Millisecond))
		}
		if sw.status >= 500 {
			captureServerError(r, id, sw.status)
		}
	})
}

// statusRecorder는 핸들러가 쓴 상태 코드와 응답 시작 여부를 기록한다
// (WriteHeader 미호출 시 200).
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
