package priceindex

import "testing"

func TestParseMonth(t *testing.T) {
	cases := map[string]string{
		"2025년 9월":   "202509",
		"2026년 6월":   "202606",
		"2026년 12월":  "202612",
		" 2026년 1월 ": "202601",
		"":           "",
		"2026":       "",
		"헤더":         "",
		"2026년 13월":  "", // 월 범위 밖
	}
	for in, want := range cases {
		if got := parseMonth(in); got != want {
			t.Errorf("parseMonth(%q)=%q want %q", in, got, want)
		}
	}
}
