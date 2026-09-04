package address

import "testing"

func TestDecomposeJamo(t *testing.T) {
	cases := map[string]string{
		"약국":   "ㅇㅑㄱㄱㅜㄱ",
		"온누리":  "ㅇㅗㄴㄴㅜㄹㅣ",
		"값":    "ㄱㅏㅄ",
		"a약1":  "aㅇㅑㄱ1",
		"세종대로": "ㅅㅔㅈㅗㅇㄷㅐㄹㅗ",
		"":     "",
	}
	for in, want := range cases {
		if got := DecomposeJamo(in); got != want {
			t.Errorf("DecomposeJamo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChoseong(t *testing.T) {
	cases := map[string]string{
		"약국":    "ㅇㄱ",
		"온누리약국": "ㅇㄴㄹㅇㄱ",
		"카페7":   "ㅋㅍ7",
		"":      "",
	}
	for in, want := range cases {
		if got := Choseong(in); got != want {
			t.Errorf("Choseong(%q) = %q, want %q", in, got, want)
		}
	}
}
