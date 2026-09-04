package juso

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 실제 juso 202605 전체분(전국 zip)의 rnaddrkor_sejong.txt / jibun_rnaddrkor_sejong.txt
// 에서 뽑은 실데이터 행이다(MS949 원본 바이트, CRLF 포함). 반곡동/한솔동/조치원읍 등
// 다양한 표기 분기를 포함하도록 20행을 선별했다.
const rnaddrkorFixture = "testdata/rnaddrkor_세종특별자치시.txt"
const jibunFixture = "testdata/jibun_rnaddrkor_세종특별자치시.txt"

func readRnaddrkor(t *testing.T) []RnaddrkorRecord {
	t.Helper()
	f, err := os.Open(rnaddrkorFixture)
	if err != nil {
		t.Fatalf("픽스처 열기: %v", err)
	}
	defer f.Close()
	var recs []RnaddrkorRecord
	if err := ParseRnaddrkor(f, func(r RnaddrkorRecord) error {
		recs = append(recs, r)
		return nil
	}); err != nil {
		t.Fatalf("ParseRnaddrkor: %v", err)
	}
	return recs
}

func TestParseRnaddrkor_MS949AndShape(t *testing.T) {
	recs := readRnaddrkor(t)
	if len(recs) != 20 {
		t.Fatalf("행 수: got %d want 20", len(recs))
	}
	for i, r := range recs {
		// MS949 디코딩 확인: 시도명은 항상 세종특별자치시.
		if r.SidoName != "세종특별자치시" {
			t.Errorf("행%d MS949 디코딩 실패: 시도명=%q", i, r.SidoName)
		}
		if len(r.LegalDongCode) != 10 {
			t.Errorf("행%d 법정동코드 길이=%d want 10", i, len(r.LegalDongCode))
		}
		if len(r.MgmtNo) != 26 {
			t.Errorf("행%d 도로명주소관리번호 길이=%d want 26", i, len(r.MgmtNo))
		}
	}
}

func TestRnaddrkorComposition(t *testing.T) {
	recs := readRnaddrkor(t)
	// 실행 위치별 기대값은 원본 컬럼에서 활용가이드 표기 규칙으로 손계산한 것이다.
	cases := []struct {
		pos        int
		road       string
		jibun      string
		buildingNm string
	}{
		// 동지역 비공동주택(참고항목=동명), 건물명은 시군구용건물명.
		{0, "세종특별자치시 한누리대로 1811 (반곡동)", "세종특별자치시 반곡동 899", "수루배마을5단지 상가동"},
		// 동지역, 건물명 없음.
		{2, "세종특별자치시 한누리대로 1824 (반곡동)", "세종특별자치시 반곡동 865", ""},
		// 산 지번(지번에 '산' 접두).
		{7, "세종특별자치시 시청대로 365 (반곡동)", "세종특별자치시 반곡동 산67", "수변공원 4-1 공중화장실"},
		// 동지역 공동주택(참고항목=동명+건물명).
		{12, "세종특별자치시 시청대로 500 (반곡동, 수루배마을 4단지)", "세종특별자치시 반곡동 857", "수루배마을 4단지"},
		{13, "세종특별자치시 시청대로 546 (반곡동, 수루배마을8단지)", "세종특별자치시 반곡동 858", "수루배마을8단지"},
		// 산 지번 + 건물부번.
		{15, "세종특별자치시 어울로2길 5-8 (한솔동)", "세종특별자치시 한솔동 산1128", ""},
		// 읍지역(법정리명 존재 → 본문에 읍면동 포함, 참고항목 없음), 지번에 리 포함.
		{16, "세종특별자치시 조치원읍 새내로 96", "세종특별자치시 조치원읍 원리 7-23", ""},
	}
	for _, c := range cases {
		r := recs[c.pos]
		if got := r.RoadAddress(); got != c.road {
			t.Errorf("pos%d RoadAddress=%q want %q", c.pos, got, c.road)
		}
		if got := r.JibunAddress(); got != c.jibun {
			t.Errorf("pos%d JibunAddress=%q want %q", c.pos, got, c.jibun)
		}
		if got := r.BuildingName(); got != c.buildingNm {
			t.Errorf("pos%d BuildingName=%q want %q", c.pos, got, c.buildingNm)
		}
	}
}

func TestParseJibun_Count(t *testing.T) {
	f, err := os.Open(jibunFixture)
	if err != nil {
		t.Fatalf("픽스처 열기: %v", err)
	}
	defer f.Close()
	var n int
	if err := ParseJibun(f, func(r JibunRecord) error {
		if r.SidoName != "세종특별자치시" {
			t.Errorf("MS949 디코딩 실패: 시도명=%q", r.SidoName)
		}
		n++
		return nil
	}); err != nil {
		t.Fatalf("ParseJibun: %v", err)
	}
	if n != 20 {
		t.Errorf("관련지번 행 수: got %d want 20", n)
	}
}

func TestParseRnaddrkor_ColumnMismatch(t *testing.T) {
	// 컬럼 수가 24가 아니면 에러로 중단해야 한다(스키마 검증).
	bad := strings.NewReader("a|b|c\n")
	err := ParseRnaddrkor(bad, func(RnaddrkorRecord) error { return nil })
	if err == nil {
		t.Fatal("컬럼 불일치인데 에러가 없다")
	}
	if !strings.Contains(err.Error(), "컬럼 수 불일치") {
		t.Errorf("예상치 못한 에러: %v", err)
	}
}

func TestSplitFields_TrailingPipeTolerated(t *testing.T) {
	// 각 행이 파이프로 끝나 필드가 하나 더 나오는 배포본을 허용한다.
	line := strings.Repeat("x|", RnaddrkorColumnCount) // 24개 필드 + 마지막 빈 필드
	f, err := splitFields(line, RnaddrkorColumnCount)
	if err != nil {
		t.Fatalf("후행 파이프 허용 실패: %v", err)
	}
	if len(f) != RnaddrkorColumnCount {
		t.Errorf("필드 수=%d want %d", len(f), RnaddrkorColumnCount)
	}
}

func TestFixtureExists(t *testing.T) {
	if _, err := os.Stat(filepath.FromSlash(rnaddrkorFixture)); err != nil {
		t.Fatalf("픽스처 없음: %v", err)
	}
}
