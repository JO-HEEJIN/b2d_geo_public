package address

import "strings"

// 자모 분해 (Phase 2-2): 완성형 한글 음절을 초/중/종성으로 분해한다.
// places 초성 검색("ㅇㄱ"→"약국")과 trigram 정규화의 기반 유틸.

var (
	choseong  = []rune("ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎ")
	jungseong = []rune("ㅏㅐㅑㅒㅓㅔㅕㅖㅗㅘㅙㅚㅛㅜㅝㅞㅟㅠㅡㅢㅣ")
	jongseong = []rune{0, 'ㄱ', 'ㄲ', 'ㄳ', 'ㄴ', 'ㄵ', 'ㄶ', 'ㄷ', 'ㄹ', 'ㄺ',
		'ㄻ', 'ㄼ', 'ㄽ', 'ㄾ', 'ㄿ', 'ㅀ', 'ㅁ', 'ㅂ', 'ㅄ', 'ㅅ', 'ㅆ',
		'ㅇ', 'ㅈ', 'ㅊ', 'ㅋ', 'ㅌ', 'ㅍ', 'ㅎ'}
)

// DecomposeJamo는 문자열의 한글 음절을 자모 나열로 분해한다.
// 비한글 문자는 그대로 통과한다. ("약국" → "ㅇㅑㄱㄱㅜㄱ")
func DecomposeJamo(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0xAC00 || r > 0xD7A3 {
			b.WriteRune(r)
			continue
		}
		n := r - 0xAC00
		b.WriteRune(choseong[n/588])
		b.WriteRune(jungseong[(n%588)/28])
		if j := jongseong[n%28]; j != 0 {
			b.WriteRune(j)
		}
	}
	return b.String()
}

// Choseong은 문자열의 한글 음절에서 초성만 뽑는다. ("약국" → "ㅇㄱ")
// 비한글 문자는 그대로 통과한다.
func Choseong(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0xAC00 || r > 0xD7A3 {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(choseong[(r-0xAC00)/588])
	}
	return b.String()
}
