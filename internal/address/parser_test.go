package address

import "testing"

func TestParseDesignTable(t *testing.T) {
	cases := []struct {
		in   string
		want Parsed
	}{
		{"서울특별시 중구 세종대로 110",
			Parsed{Sido: "서울특별시", Sigungu: "중구", Road: "세종대로", BldNo: "110", Kind: Road}},
		{"서울 중구 세종대로 110",
			Parsed{Sido: "서울특별시", Sigungu: "중구", Road: "세종대로", BldNo: "110", Kind: Road}},
		{"중구 세종대로 110",
			Parsed{Sigungu: "중구", Road: "세종대로", BldNo: "110", Kind: Road}},
		{"서울특별시 중구 세종대로 지하 110",
			Parsed{Sido: "서울특별시", Sigungu: "중구", Road: "세종대로", Under: true, BldNo: "110", Kind: Road}},
		{"세종대로 110-2",
			Parsed{Road: "세종대로", BldNo: "110-2", Kind: Road}},
		{"서울 은평구 수색동 179-3",
			Parsed{Sido: "서울특별시", Sigungu: "은평구", Emd: "수색동", Jibun: "179-3", Kind: Jibun}},
		{"은평구 수색동 산79-3",
			Parsed{Sigungu: "은평구", Emd: "수색동", Jibun: "79-3", San: true, Kind: Jibun}},
		{"수색동 179-3번지",
			Parsed{Emd: "수색동", Jibun: "179-3", Kind: Jibun}},
		{"성동구 성수동1가 685-142",
			Parsed{Sigungu: "성동구", Emd: "성수동1가", Jibun: "685-142", Kind: Jibun}},
		{"종로구 종로 1",
			Parsed{Sigungu: "종로구", Road: "종로", BldNo: "1", Kind: Road}},
		{"경기도 성남시 분당구 판교역로 166",
			Parsed{Sido: "경기도", Sigungu: "성남시 분당구", Road: "판교역로", BldNo: "166", Kind: Road}},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if got.Sido != c.want.Sido || got.Sigungu != c.want.Sigungu ||
			got.Emd != c.want.Emd || got.Road != c.want.Road ||
			got.Under != c.want.Under || got.BldNo != c.want.BldNo ||
			got.Jibun != c.want.Jibun || got.San != c.want.San ||
			got.Kind != c.want.Kind {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

func TestParseAdminDongWarning(t *testing.T) {
	got := Parse("성동구 성수1동 685")
	if got.Emd != "성수1동" || len(got.Warnings) == 0 {
		t.Errorf("행정동 경고 기대: %+v", got)
	}
}

func TestParseParenExtra(t *testing.T) {
	got := Parse("서울특별시 중구 세종대로 110 (태평로1가)")
	if got.Extra != "(태평로1가)" || got.BldNo != "110" || got.Kind != Road {
		t.Errorf("괄호 격리 실패: %+v", got)
	}
}

func TestRoadKey(t *testing.T) {
	p := Parse("서울 중구 세종대로 110")
	key, full := p.RoadKey()
	if key != "서울특별시 중구 세종대로 110" || !full {
		t.Errorf("RoadKey = %q, full=%v", key, full)
	}
	p2 := Parse("중구 세종대로 지하 110")
	key2, full2 := p2.RoadKey()
	if key2 != "중구 세종대로 지하 110" || full2 {
		t.Errorf("RoadKey 부분키 = %q, full=%v", key2, full2)
	}
}

func TestParseGarbage(t *testing.T) {
	for _, in := range []string{"", "12345", "!!! ???", "동"} {
		got := Parse(in)
		if got.Kind == Road || got.Kind == Jibun {
			if got.Road == "" && got.Emd == "" {
				t.Errorf("Parse(%q) 비정상 확정: %+v", in, got)
			}
		}
	}
}
