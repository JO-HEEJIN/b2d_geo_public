package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashKeyDeterministic(t *testing.T) {
	h1 := HashKey("b2d_secret")
	h2 := HashKey("b2d_secret")
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("sha-256 hex must be 64 chars, got %d", len(h1))
	}
	if HashKey("other") == h1 {
		t.Fatal("distinct inputs hashed equal")
	}
}

func TestGenerateKey(t *testing.T) {
	pt, prefix, hash, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if prefix != pt[:keyPrefixLen] {
		t.Fatalf("prefix %q must be first %d of plaintext %q", prefix, keyPrefixLen, pt)
	}
	if hash != HashKey(pt) {
		t.Fatal("hash mismatch")
	}
	pt2, _, _, _ := GenerateKey()
	if pt == pt2 {
		t.Fatal("two generated keys collided")
	}
}

func TestParseScopes(t *testing.T) {
	got, err := ParseScopes("geo, realestate ,geo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "geo" || got[1] != "realestate" {
		t.Fatalf("dedup/trim failed: %v", got)
	}
	if _, err := ParseScopes("bogus"); err == nil {
		t.Fatal("expected error for unknown scope")
	}
	if _, err := ParseScopes(" , "); err == nil {
		t.Fatal("expected error for empty scopes")
	}
}

func TestRequiredScope(t *testing.T) {
	cases := map[string]string{
		"/v1/geocode":               "geo",
		"/v1/parcel/4145010100":     "geo",
		"/v1/appraisal/sales":       "realestate",
		"/v1/appraisal/price-index": "realestate",
	}
	for path, want := range cases {
		if got := requiredScope(path); got != want {
			t.Errorf("requiredScope(%q)=%q want %q", path, got, want)
		}
	}
}

func TestHasScope(t *testing.T) {
	scopes := []string{"geo"}
	if !hasScope(scopes, "geo") {
		t.Error("geo should be present")
	}
	if hasScope(scopes, "realestate") {
		t.Error("realestate should be absent")
	}
}

func TestExtractKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/geocode", nil)
	r.Header.Set("X-API-Key", "fromheader")
	if got := extractKey(r); got != "fromheader" {
		t.Errorf("X-API-Key priority: got %q", got)
	}

	r2 := httptest.NewRequest("GET", "/v1/geocode", nil)
	r2.Header.Set("Authorization", "Bearer bearerkey")
	if got := extractKey(r2); got != "bearerkey" {
		t.Errorf("bearer fallback: got %q", got)
	}

	r3 := httptest.NewRequest("GET", "/v1/geocode", nil)
	if got := extractKey(r3); got != "" {
		t.Errorf("no header should be empty: got %q", got)
	}
}

func TestCacheTTLFromEnv(t *testing.T) {
	t.Setenv("B2D_AUTH_CACHE_TTL", "") // 미설정 → 기본값
	if got := cacheTTLFromEnv(); got != cacheTTL {
		t.Errorf("unset should default to %v: got %v", cacheTTL, got)
	}
	t.Setenv("B2D_AUTH_CACHE_TTL", "5s")
	if got := cacheTTLFromEnv(); got != 5*time.Second {
		t.Errorf("5s: got %v", got)
	}
	t.Setenv("B2D_AUTH_CACHE_TTL", "0s") // 온프레미스: 캐시 무효, revoke 즉시
	if got := cacheTTLFromEnv(); got != 0 {
		t.Errorf("0s: got %v", got)
	}
	t.Setenv("B2D_AUTH_CACHE_TTL", "garbage") // 파싱실패 → 폴백
	if got := cacheTTLFromEnv(); got != cacheTTL {
		t.Errorf("garbage should fall back: got %v", got)
	}
	t.Setenv("B2D_AUTH_CACHE_TTL", "-5s") // 음수 → 폴백
	if got := cacheTTLFromEnv(); got != cacheTTL {
		t.Errorf("negative should fall back: got %v", got)
	}
}

func TestTokenBucketBurstThenDeny(t *testing.T) {
	l := newLimiter()
	now := time.Now()
	// rate=10, burst=20: 첫 20건 허용, 21번째 거부.
	for i := 0; i < 20; i++ {
		if ok, _ := l.allow("k", 10, 20, now); !ok {
			t.Fatalf("request %d should be allowed within burst", i)
		}
	}
	ok, wait := l.allow("k", 10, 20, now)
	if ok {
		t.Fatal("21st request should be denied")
	}
	if wait <= 0 {
		t.Fatal("denied request must report positive retry wait")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	l := newLimiter()
	now := time.Now()
	for i := 0; i < 20; i++ {
		l.allow("k", 10, 20, now)
	}
	if ok, _ := l.allow("k", 10, 20, now); ok {
		t.Fatal("bucket should be empty")
	}
	// 1초 경과 → rate=10 토큰 리필 → 다시 허용.
	later := now.Add(time.Second)
	if ok, _ := l.allow("k", 10, 20, later); !ok {
		t.Fatal("after 1s refill should allow again")
	}
}

func TestValidTier(t *testing.T) {
	for _, tier := range []string{"demo", "standard"} {
		if !ValidTier(tier) {
			t.Errorf("ValidTier(%q) = false, want true", tier)
		}
	}
	for _, tier := range []string{"", "free", "pro", "DEMO"} {
		if ValidTier(tier) {
			t.Errorf("ValidTier(%q) = true, want false", tier)
		}
	}
}
