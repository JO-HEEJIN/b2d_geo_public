package juso

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// RnaddrkorRecord는 도로명주소 한글 한 행이다. 모든 필드는 파일 원문(문자열)을
// 그대로 담는다. 숫자 필드도 원문 보존을 위해 문자열로 둔다.
type RnaddrkorRecord struct {
	MgmtNo         string // 도로명주소관리번호 (PK, buildings 자연키)
	LegalDongCode  string // 법정동코드 (buildings.bcode)
	SidoName       string
	SigunguName    string
	LegalEmdName   string // 법정읍면동명
	LegalRiName    string // 법정리명
	MountainYN     string // 산여부 0:대지, 1:산
	JibunMain      string
	JibunSub       string
	RoadCode       string
	RoadName       string
	UndergroundYN  string // 지하여부 0:지상, 1:지하, 2:공중, 3:수상
	BuildingMain   string
	BuildingSub    string
	ZoneNo         string // 기초구역번호(우편번호)
	ChangeReason   string // 이동사유코드 (변동분에서만 유효)
	LedgerBuildNm  string // 건축물대장건물명
	SigunguBuildNm string // 시군구용건물명
	ApartmentClass string // 공동주택구분
}

// JibunRecord는 관련지번 한 행이다.
type JibunRecord struct {
	MgmtNo        string // 도로명주소관리번호 (한글 레코드와 조인 키)
	LegalDongCode string
	SidoName      string
	SigunguName   string
	LegalEmdName  string
	LegalRiName   string
	MountainYN    string
	JibunMain     string
	JibunSub      string
	ChangeReason  string
}

// decodingScanner는 MS949(CP949) 바이트를 UTF-8로 디코딩하며 한 줄씩 읽는 스캐너다.
// juso 파일은 헤더가 없고 파이프 구분자를 쓴다(docs/dataspec/juso.md §1).
func decodingScanner(r io.Reader) *bufio.Scanner {
	dec := transform.NewReader(r, korean.EUCKR.NewDecoder())
	sc := bufio.NewScanner(dec)
	// 도로명주소 한글 행은 이전도로명주소(400)/건물명(400) 등으로 길 수 있어 버퍼를 키운다.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return sc
}

// splitFields는 한 줄을 파이프로 분할하고 컬럼 수를 검증한다. 일부 배포본은 각 행을
// 파이프로 끝맺어 필드가 하나 더 나올 수 있으므로 마지막 빈 필드는 허용한다.
func splitFields(line string, want int) ([]string, error) {
	line = strings.TrimRight(line, "\r")
	f := strings.Split(line, string(Delimiter))
	if len(f) == want+1 && f[len(f)-1] == "" {
		f = f[:want]
	}
	if len(f) != want {
		return nil, fmt.Errorf("컬럼 수 불일치: got %d want %d", len(f), want)
	}
	return f, nil
}

// rnaddrkorFromFields는 검증된 24개 필드를 RnaddrkorRecord로 매핑한다.
// ParseRnaddrkor와 로더가 공유한다.
func rnaddrkorFromFields(f []string) RnaddrkorRecord {
	return RnaddrkorRecord{
		MgmtNo:         f[rkMgmtNo],
		LegalDongCode:  f[rkLegalDongCode],
		SidoName:       f[rkSidoName],
		SigunguName:    f[rkSigunguName],
		LegalEmdName:   f[rkLegalEmdName],
		LegalRiName:    f[rkLegalRiName],
		MountainYN:     f[rkMountainYN],
		JibunMain:      f[rkJibunMain],
		JibunSub:       f[rkJibunSub],
		RoadCode:       f[rkRoadCode],
		RoadName:       f[rkRoadName],
		UndergroundYN:  f[rkUndergroundYN],
		BuildingMain:   f[rkBuildingMain],
		BuildingSub:    f[rkBuildingSub],
		ZoneNo:         f[rkZoneNo],
		ChangeReason:   f[rkChangeReason],
		LedgerBuildNm:  f[rkLedgerBuildName],
		SigunguBuildNm: f[rkSigunguBuildNm],
		ApartmentClass: f[rkApartmentClass],
	}
}

// ParseRnaddrkor는 도로명주소 한글 파일을 스트리밍 파싱하고 각 행마다 fn을 호출한다.
// 컬럼 수가 24가 아니면 해당 행 번호와 함께 에러를 반환하고 중단한다(적재 중단 원칙).
func ParseRnaddrkor(r io.Reader, fn func(RnaddrkorRecord) error) error {
	sc := decodingScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if text == "" {
			continue
		}
		f, err := splitFields(text, RnaddrkorColumnCount)
		if err != nil {
			return fmt.Errorf("도로명주소 한글 %d행: %w", line, err)
		}
		if err := fn(rnaddrkorFromFields(f)); err != nil {
			return err
		}
	}
	return sc.Err()
}

// ParseJibun는 관련지번 파일을 스트리밍 파싱한다.
func ParseJibun(r io.Reader, fn func(JibunRecord) error) error {
	sc := decodingScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if text == "" {
			continue
		}
		f, err := splitFields(text, JibunColumnCount)
		if err != nil {
			return fmt.Errorf("관련지번 %d행: %w", line, err)
		}
		rec := JibunRecord{
			MgmtNo:        f[jbMgmtNo],
			LegalDongCode: f[jbLegalDongCode],
			SidoName:      f[jbSidoName],
			SigunguName:   f[jbSigunguName],
			LegalEmdName:  f[jbLegalEmdName],
			LegalRiName:   f[jbLegalRiName],
			MountainYN:    f[jbMountainYN],
			JibunMain:     f[jbJibunMain],
			JibunSub:      f[jbJibunSub],
			ChangeReason:  f[jbChangeReason],
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return sc.Err()
}

// atoi는 숫자 필드를 정수로 바꾼다. 빈 값/비정상 값은 0으로 취급한다.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// RoadAddress는 컬럼으로부터 도로명주소 문자열을 조합한다.
// 조합 규칙: docs/dataspec/juso.md §2 및 활용가이드 rnadr_korDBGuide.pdf 붙임1/붙임2.
//  1. 법정리명이 있으면 법정읍면동명을 본문에 포함(면지역/읍지역).
//  2. 법정리명이 없고 동지역이면 읍면동명은 참고항목으로.
//  3. 건물부번이 0이면 표기하지 않음.
//  4. 공동주택이면 참고항목에 시군구용건물명 포함.
//  5. 지하여부가 1이면 건물본번 앞에 '지하'.
func (r RnaddrkorRecord) RoadAddress() string {
	var b strings.Builder
	b.WriteString(r.SidoName)
	if r.SigunguName != "" {
		b.WriteByte(' ')
		b.WriteString(r.SigunguName)
	}
	if r.LegalRiName != "" {
		b.WriteByte(' ')
		b.WriteString(r.LegalEmdName)
	}
	b.WriteByte(' ')
	b.WriteString(r.RoadName)
	b.WriteByte(' ')
	if r.UndergroundYN == "1" {
		b.WriteString("지하")
	}
	b.WriteString(strconv.Itoa(atoi(r.BuildingMain)))
	if sub := atoi(r.BuildingSub); sub != 0 {
		b.WriteByte('-')
		b.WriteString(strconv.Itoa(sub))
	}
	if ref := r.referenceItem(); ref != "" {
		b.WriteByte(' ')
		b.WriteString(ref)
	}
	return b.String()
}

// referenceItem은 도로명주소 괄호 참고항목을 만든다(법정동/공동주택명).
func (r RnaddrkorRecord) referenceItem() string {
	isDong := r.LegalRiName == "" && strings.HasSuffix(r.LegalEmdName, "동")
	apt := r.ApartmentClass == "1"
	name := r.SigunguBuildNm
	switch {
	case isDong && apt && name != "":
		return "(" + r.LegalEmdName + ", " + name + ")"
	case isDong:
		return "(" + r.LegalEmdName + ")"
	case apt && name != "":
		return "(" + name + ")"
	default:
		return ""
	}
}

// JibunAddress는 컬럼으로부터 지번주소 문자열을 조합한다.
func (r RnaddrkorRecord) JibunAddress() string {
	var b strings.Builder
	b.WriteString(r.SidoName)
	if r.SigunguName != "" {
		b.WriteByte(' ')
		b.WriteString(r.SigunguName)
	}
	if r.LegalEmdName != "" {
		b.WriteByte(' ')
		b.WriteString(r.LegalEmdName)
	}
	if r.LegalRiName != "" {
		b.WriteByte(' ')
		b.WriteString(r.LegalRiName)
	}
	b.WriteByte(' ')
	if r.MountainYN == "1" {
		b.WriteString("산")
	}
	b.WriteString(strconv.Itoa(atoi(r.JibunMain)))
	if sub := atoi(r.JibunSub); sub != 0 {
		b.WriteByte('-')
		b.WriteString(strconv.Itoa(sub))
	}
	return b.String()
}

// BuildingName은 대표 건물명을 고른다. 시군구용건물명 우선, 없으면 건축물대장건물명.
// 둘 다 없으면 빈 문자열(buildings.building_name은 NULL 허용).
func (r RnaddrkorRecord) BuildingName() string {
	if r.SigunguBuildNm != "" {
		return r.SigunguBuildNm
	}
	return r.LedgerBuildNm
}
