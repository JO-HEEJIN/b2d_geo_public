package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 요청 ID 미들웨어: 응답 헤더에 X-Request-Id가 붙고, 핸들러 컨텍스트에서
// RequestID로 같은 값을 읽을 수 있어야 한다.
func TestRequestIDMiddleware(t *testing.T) {
	var seen string
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	got := rec.Header().Get("X-Request-Id")
	if len(got) != 12 {
		t.Fatalf("X-Request-Id = %q, want 12-hex", got)
	}
	if seen != got {
		t.Fatalf("context id %q != header id %q", seen, got)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d (statusRecorder 전달 오류)", rec.Code, http.StatusTeapot)
	}
}

// 서로 다른 요청은 서로 다른 ID를 받아야 한다.
func TestRequestIDUnique(t *testing.T) {
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
		ids[rec.Header().Get("X-Request-Id")] = true
	}
	if len(ids) != 100 {
		t.Fatalf("unique ids = %d, want 100", len(ids))
	}
}
