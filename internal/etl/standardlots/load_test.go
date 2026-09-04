package standardlots

import "testing"

func TestDateDashes(t *testing.T) {
	cases := map[string]string{
		"20260515": "2026-05-15",
		"20260101": "2026-01-01",
		"":         "",
		"2026051":  "", // 7자리
		"2026x515": "", // 비숫자
	}
	for in, want := range cases {
		if got := dateDashes(in); got != want {
			t.Errorf("dateDashes(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNz(t *testing.T) {
	if nz("") != nil {
		t.Error("빈 문자열은 nil이어야")
	}
	if nz("대") != "대" {
		t.Error("비어있지 않으면 원문")
	}
}
