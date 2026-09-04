package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// mockTransport는 전송 대신 이벤트를 메모리에 쌓는다.
type mockTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *mockTransport) Configure(sentry.ClientOptions) {}
func (t *mockTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}
func (t *mockTransport) Flush(time.Duration) bool              { return true }
func (t *mockTransport) FlushWithContext(context.Context) bool { return true }
func (t *mockTransport) Close()                                {}

func (t *mockTransport) all() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

const fakeSecret = "super-secret-molit-key=="

func initTestSentry(t *testing.T) *mockTransport {
	t.Helper()
	tr := &mockTransport{}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              "https://public@sentry.example.invalid/1",
		Transport:        tr,
		AttachStacktrace: true,
		BeforeSend:       SentryScrubber(fakeSecret),
	})
	if err != nil {
		t.Fatalf("sentry init: %v", err)
	}
	// 테스트 간 오염 방지: 종료 시 클라이언트 해제.
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return tr
}

// panic이 500 응답으로 복구되고, request_id 태그가 붙은 이벤트가 수집되며,
// 비밀값이 스크럽되는지 한 번에 검증한다.
func TestPanicCapturedWithRequestID(t *testing.T) {
	tr := initTestSentry(t)
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom with " + fakeSecret)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/geocode?q=x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	id := rec.Header().Get("X-Request-Id")
	events := tr.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Tags["request_id"] != id {
		t.Fatalf("request_id tag %q != header %q", e.Tags["request_id"], id)
	}
	if e.Tags["http.method"] != "GET" || e.Tags["url.path"] != "/v1/geocode" ||
		e.Tags["http.status_code"] != "500" {
		t.Fatalf("tags incomplete: %v", e.Tags)
	}
	if len(e.Exception) == 0 || e.Exception[0].Stacktrace == nil {
		t.Fatalf("exception/stacktrace missing")
	}
	if strings.Contains(e.Exception[0].Value, fakeSecret) {
		t.Fatalf("secret leaked into event: %q", e.Exception[0].Value)
	}
	if !strings.Contains(e.Exception[0].Value, "***") {
		t.Fatalf("secret not scrubbed: %q", e.Exception[0].Value)
	}
	if e.Request != nil {
		t.Fatalf("request payload must not be sent")
	}
}

// 5xx 응답은 request_id 태그가 붙은 메시지 이벤트로 남아야 한다.
func TestServerErrorCaptured(t *testing.T) {
	tr := initTestSentry(t)
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadGateway, "upstream unavailable")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/appraisal/sales?lawd_cd=41450&deal_ymd=202401", nil))

	events := tr.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Tags["http.status_code"] != "502" || e.Tags["request_id"] == "" {
		t.Fatalf("tags incomplete: %v", e.Tags)
	}
	if strings.Contains(e.Message, "lawd_cd") {
		t.Fatalf("query string must not be sent: %q", e.Message)
	}
}

// 4xx는 Sentry로 보내지 않는다 (로그만).
func TestClientErrorNotCaptured(t *testing.T) {
	tr := initTestSentry(t)
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "bad")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if n := len(tr.all()); n != 0 {
		t.Fatalf("events = %d, want 0", n)
	}
}

// Sentry 미초기화(클라이언트 없음)여도 panic 복구·500 응답은 동일하게 동작한다.
func TestPanicRecoveryWithoutSentry(t *testing.T) {
	sentry.CurrentHub().BindClient(nil)
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("X-Request-Id missing")
	}
}
