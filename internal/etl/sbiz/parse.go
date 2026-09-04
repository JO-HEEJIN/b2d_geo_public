package sbiz

import (
	"strconv"
	"strings"
)

// stagedRow는 places 적재용으로 정제한 한 행이다. lon/lat은 staging에 실수로
// 적재되고, 최종 INSERT에서 ST_MakePoint로 geom(4326)을 구성한다.
type stagedRow struct {
	storeNumber string
	name        string
	category    string // 상권업종소분류코드 (빈 문자열이면 최종적으로 NULL)
	jibun       string
	road        string
	normName    string
	lon         float64
	lat         float64
}

// parseRow는 CSV 한 행을 stagedRow로 변환한다. 두 번째 반환값이 false면
// 적재 대상에서 제외(스킵)한다. 스킵 사유: 컬럼 수 불일치, 상가업소번호 없음,
// 경위도 파싱 실패 또는 한반도 bbox 밖.
func parseRow(rec []string) (stagedRow, bool) {
	if len(rec) != ColumnCount {
		return stagedRow{}, false
	}
	storeNumber := strings.TrimSpace(rec[colStoreNumber])
	if storeNumber == "" {
		return stagedRow{}, false
	}
	lon, err := strconv.ParseFloat(strings.TrimSpace(rec[colLongitude]), 64)
	if err != nil {
		return stagedRow{}, false
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(rec[colLatitude]), 64)
	if err != nil {
		return stagedRow{}, false
	}
	if lon < LonMin || lon > LonMax || lat < LatMin || lat > LatMax {
		return stagedRow{}, false
	}
	name := rec[colName]
	return stagedRow{
		storeNumber: storeNumber,
		name:        name,
		category:    strings.TrimSpace(rec[colSmlCategoryCode]),
		jibun:       rec[colJibunAddress],
		road:        rec[colRoadAddress],
		normName:    normalizeName(name),
		lon:         lon,
		lat:         lat,
	}, true
}

// normalizeName은 상호명 정규화 자리표시자다. 지금은 앞뒤 공백 제거 + 내부
// 연속 공백 단일화만 수행한다.
// TODO(Phase 2): 실제 자모 분해/정규화는 internal/address에서 구현하고 교체한다.
func normalizeName(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
