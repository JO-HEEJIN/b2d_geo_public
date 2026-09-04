// Sentry 연동 헬퍼. 원칙:
//   - SENTRY_DSN 미설정이면 클라이언트가 바인딩되지 않아 아래 함수 전부 no-op —
//     서버는 기존 로그만으로 동작한다.
//   - 전송은 SDK 백그라운드 큐라 요청 처리를 막지 않는다.
//   - 전송 범위 최소화: 태그(request_id·메서드·경로·상태코드)만 싣고 헤더·쿼리스트링·
//     바디·IP는 싣지 않는다 (주소 질의 등 민감 파라미터 보호). Scrub이 이중 방어.
package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getsentry/sentry-go"
)

// sentryScope는 요청 공통 태그를 스코프에 채운다.
func sentryScope(scope *sentry.Scope, r *http.Request, id string, status int) {
	scope.SetTag("request_id", id)
	scope.SetTag("http.method", r.Method)
	scope.SetTag("url.path", r.URL.Path)
	scope.SetTag("http.status_code", strconv.Itoa(status))
}

// capturePanic는 recover된 panic을 요청 태그와 함께 Sentry로 보낸다 (레벨 fatal,
// 스택 포함). panic 값이 error가 아니면 error로 감싼다 — SDK가 string panic을
// Message로만 만들면 Exception 스택 그룹핑이 안 되기 때문. 실패해도 요청 처리에
// 영향 없음.
func capturePanic(r *http.Request, id string, status int, rec any) {
	err, ok := rec.(error)
	if !ok {
		err = fmt.Errorf("panic: %v", rec)
	}
	hub := sentry.CurrentHub().Clone()
	hub.WithScope(func(scope *sentry.Scope) {
		sentryScope(scope, r, id, status)
		scope.SetLevel(sentry.LevelFatal)
		hub.RecoverWithContext(r.Context(), err)
	})
}

// captureServerError는 5xx 응답을 이벤트로 남긴다 (핸들러가 writeErr로 처리한
// "정상 흐름의 서버 에러"라 스택은 없고, request_id로 서버 로그와 대조한다).
func captureServerError(r *http.Request, id string, status int) {
	hub := sentry.CurrentHub().Clone()
	hub.WithScope(func(scope *sentry.Scope) {
		sentryScope(scope, r, id, status)
		scope.SetLevel(sentry.LevelError)
		hub.CaptureMessage(fmt.Sprintf("%s %s -> %d", r.Method, r.URL.Path, status))
	})
}

// SentryScrubber는 BeforeSend 훅: 비밀값(원문·URL인코딩)을 ***로 치환하고,
// 요청 상세(헤더·쿼리·바디)와 사용자 정보(IP 등)를 이벤트에서 제거한다.
// main에서 MOLIT_API_KEY 등 프로세스가 아는 비밀값을 넘긴다.
func SentryScrubber(secrets ...string) func(*sentry.Event, *sentry.EventHint) *sentry.Event {
	pairs := make([]string, 0, len(secrets)*4)
	for _, s := range secrets {
		if s == "" {
			continue // 빈 문자열이면 ReplaceAll이 모든 문자 사이에 삽입되므로 제외
		}
		pairs = append(pairs, s, "***", url.QueryEscape(s), "***")
	}
	repl := strings.NewReplacer(pairs...)
	return func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
		event.Request = nil
		event.User = sentry.User{}
		event.Message = repl.Replace(event.Message)
		for i := range event.Exception {
			event.Exception[i].Value = repl.Replace(event.Exception[i].Value)
		}
		for i := range event.Breadcrumbs {
			event.Breadcrumbs[i].Message = repl.Replace(event.Breadcrumbs[i].Message)
		}
		return event
	}
}
