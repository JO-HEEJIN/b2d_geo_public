package address

import (
	"regexp"
	"strings"
)

// Kind는 파싱된 주소 유형이다.
type Kind int

const (
	Ambiguous Kind = iota // 판정 불가 — 매칭 단계에서 양 경로 시도
	Road                  // 도로명주소
	Jibun                 // 지번(구)주소
)

// Parsed는 파서 출력이다. 파서는 해석만 하고 추측 보정하지 않는다.
type Parsed struct {
	Sido     string
	Sigungu  string
	Emd      string
	Road     string
	Under    bool
	BldNo    string
	Jibun    string
	San      bool
	Extra    string
	Kind     Kind
	Warnings []string
}

var (
	parenRe = regexp.MustCompile(`\([^)]*\)`)
	numRe   = regexp.MustCompile(`^(산)?(\d+)(-\d+)?(번지)?$`)
)

// Parse는 자유 입력 주소를 구조화한다.
func Parse(input string) Parsed {
	var p Parsed

	// 전처리: 괄호 격리, 구두점 제거
	s := strings.NewReplacer("，", ",", "．", ".", "　", " ").Replace(input)
	if m := parenRe.FindAllString(s, -1); len(m) > 0 {
		p.Extra = strings.Join(m, " ")
		s = parenRe.ReplaceAllString(s, " ")
	}
	s = strings.NewReplacer(",", " ", ".", " ").Replace(s)

	toks := splitGlued(strings.Fields(s))
	i := 0

	// SIDO
	if i < len(toks) {
		if sido, ok := canonSido(toks[i]); ok {
			p.Sido = sido
			i++
		} else {
			p.warn("시도 생략 또는 미인식")
		}
	}

	// SIGUNGU (최대 2어절: "성남시 분당구")
	for n := 0; n < 2 && i < len(toks); n++ {
		t := toks[i]
		if hasAnySuffix(t, "시", "군", "구") && !numRe.MatchString(t) && len([]rune(t)) >= 2 {
			if p.Sigungu != "" {
				p.Sigungu += " "
			}
			p.Sigungu += t
			i++
			continue
		}
		break
	}

	// {EMD | ROAD} — 연속 소비하며 판정
	for i < len(toks) {
		t := toks[i]
		if t == "지하" {
			p.Under = true
			i++
			continue
		}
		if numRe.MatchString(t) {
			break // 숫자부 진입
		}
		switch classifyName(t) {
		case Road:
			if p.Road != "" {
				p.Road += " "
			}
			p.Road += t
			p.Kind = Road
		case Jibun:
			if p.Emd != "" {
				p.Emd += " "
			}
			p.Emd += t
			if p.Kind != Road {
				p.Kind = Jibun
			}
			if adminDongRe.MatchString(t) {
				p.warn("행정동 표기 가능성: " + t + " — 법정동 대조 필요")
			}
		default:
			// 판정 불가 토큰: 잔여로
			if p.Extra != "" {
				p.Extra += " "
			}
			p.Extra += t
			p.warn("미판정 토큰: " + t)
		}
		i++
	}

	// NUM
	if i < len(toks) {
		if m := numRe.FindStringSubmatch(toks[i]); m != nil {
			num := m[2] + m[3]
			if m[1] == "산" {
				p.San = true
			}
			if m[4] == "번지" && p.Kind == Road {
				p.warn("도로명 문맥에 '번지' 접미")
			}
			switch p.Kind {
			case Road:
				p.BldNo = num
			case Jibun:
				p.Jibun = num
			default:
				p.warn("숫자부 문맥 불명")
				p.BldNo, p.Jibun = num, num
			}
			i++
		}
	}

	// 잔여 토큰 (건물명/동호수 추정)
	if i < len(toks) {
		rest := strings.Join(toks[i:], " ")
		if p.Extra != "" {
			p.Extra += " "
		}
		p.Extra += rest
	}

	if p.Kind == Road && p.Road == "" || p.Kind == Jibun && p.Emd == "" {
		p.Kind = Ambiguous
	}
	return p
}

// gluedRe: 도로명에 건물번호가 붙은 토큰 ("시덕로249", "고덕로1길12-3").
// greedy 프리픽스가 "고덕로1길" 같은 숫자 포함 도로명을 보존한다.
var gluedRe = regexp.MustCompile(`^(.+(?:로|길))(\d+(?:-\d+)?)$`)

// splitGlued는 붙은 도로명+번호 토큰을 두 토큰으로 나눈다.
func splitGlued(toks []string) []string {
	out := make([]string, 0, len(toks)+2)
	for _, t := range toks {
		if m := gluedRe.FindStringSubmatch(t); m != nil {
			out = append(out, m[1], m[2])
			continue
		}
		out = append(out, t)
	}
	return out
}

// adminDongRe: "성수1동"류 — 숫자+동 (행정동 표기 관례).
var adminDongRe = regexp.MustCompile(`\d+동$`)

// jibunGaRe: "성수동1가", "태평로1가", "종로1가" — 법정동 '가' 계열.
var jibunGaRe = regexp.MustCompile(`\d+가$`)

// classifyName은 지명 토큰을 도로명/법정동/불명으로 판정한다.
func classifyName(t string) Kind {
	r := []rune(t)
	if len(r) < 2 {
		return Ambiguous
	}
	// '가' 계열 법정동이 도로명 접미 판정보다 우선 ("태평로1가")
	if jibunGaRe.MatchString(t) {
		return Jibun
	}
	switch r[len(r)-1] {
	case '로', '길':
		return Road
	case '동', '읍', '면', '리', '가':
		return Jibun
	}
	return Ambiguous
}

// RoadKey는 buildings.norm_road 관례(어절 단일 공백)의 1단 매칭 키를 만든다.
// 시도 생략 입력이면 두 번째 반환값이 false다 (복수 시도 후보 조회 필요).
func (p Parsed) RoadKey() (string, bool) {
	if p.Road == "" || p.BldNo == "" {
		return "", false
	}
	parts := []string{}
	if p.Sido != "" {
		parts = append(parts, p.Sido)
	}
	if p.Sigungu != "" {
		parts = append(parts, p.Sigungu)
	}
	// 군 지역 도로명주소는 읍·면이 정식 구성요소다 ("완도군 완도읍 회룡길 64")
	if p.Emd != "" && hasAnySuffix(p.Emd, "읍", "면") {
		parts = append(parts, p.Emd)
	}
	parts = append(parts, p.Road)
	if p.Under {
		parts = append(parts, "지하")
	}
	parts = append(parts, p.BldNo)
	return strings.Join(parts, " "), p.Sido != ""
}

func (p *Parsed) warn(msg string) { p.Warnings = append(p.Warnings, msg) }

func hasAnySuffix(s string, sfx ...string) bool {
	for _, x := range sfx {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}
