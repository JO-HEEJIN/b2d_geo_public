package sbiz

import (
	"encoding/csv"
	"os"
	"testing"
)

// TestParseRow_RealFixture는 세종 실측 20행(testdata)이 모두 정상 파싱되고
// 좌표가 한반도 bbox 안에 있는지 확인한다.
func TestParseRow_RealFixture(t *testing.T) {
	f, err := os.Open("testdata/sejong_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHeader(header); err != nil {
		t.Fatalf("헤더 검증 실패: %v", err)
	}

	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 20 {
		t.Fatalf("fixture 행 수 = %d, want 20", len(rows))
	}

	for i, rec := range rows {
		row, ok := parseRow(rec)
		if !ok {
			t.Errorf("행 %d 파싱 실패(스킵됨)", i)
			continue
		}
		if row.storeNumber == "" {
			t.Errorf("행 %d: 상가업소번호 비어있음", i)
		}
		if row.lon < LonMin || row.lon > LonMax || row.lat < LatMin || row.lat > LatMax {
			t.Errorf("행 %d: 좌표 bbox 밖 (%.6f, %.6f)", i, row.lon, row.lat)
		}
		// 세종은 대략 경도 127, 위도 36.4~36.8.
		if row.lon < 127.0 || row.lon > 127.6 {
			t.Errorf("행 %d: 세종 경도 이상 %.6f", i, row.lon)
		}
	}
}

func TestParseRow_Skips(t *testing.T) {
	base := make([]string, ColumnCount)
	for i := range base {
		base[i] = "x"
	}
	base[colStoreNumber] = "MA000"
	base[colLongitude] = "127.3"
	base[colLatitude] = "36.6"

	valid := append([]string(nil), base...)
	if _, ok := parseRow(valid); !ok {
		t.Fatal("유효 행이 스킵됨")
	}

	cases := map[string]func([]string){
		"컬럼수부족":   nil, // 아래에서 별도 처리
		"업소번호없음":  func(r []string) { r[colStoreNumber] = "" },
		"경도파싱실패":  func(r []string) { r[colLongitude] = "" },
		"경도bbox밖": func(r []string) { r[colLongitude] = "150.0" },
		"위도bbox밖": func(r []string) { r[colLatitude] = "10.0" },
	}
	for name, mut := range cases {
		if mut == nil {
			if _, ok := parseRow(base[:10]); ok {
				t.Errorf("%s: 스킵되어야 함", name)
			}
			continue
		}
		rec := append([]string(nil), base...)
		mut(rec)
		if _, ok := parseRow(rec); ok {
			t.Errorf("%s: 스킵되어야 함", name)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"  레인 카페  ":  "레인 카페",
		"레인   카페":    "레인 카페",
		"레인\t카페\n분점": "레인 카페 분점",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
