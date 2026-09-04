package sbiz

// 상가(상권)정보 CSV 컬럼 명세. 근거: docs/dataspec/sbiz.md (2026-03-31 실측).
// CSV는 UTF-8(BOM 없음), 쉼표 구분, 39개 컬럼 고정 순서다.

// ColumnCount는 CSV 한 행의 컬럼 수다. 헤더 검증에 사용한다.
const ColumnCount = 39

// 컬럼 인덱스(0-based). docs/dataspec/sbiz.md의 1-based 번호와 대응한다.
const (
	colStoreNumber     = 0  // 상가업소번호
	colName            = 1  // 상호명
	colBranchName      = 2  // 지점명
	colBigCategoryCode = 3  // 상권업종대분류코드
	colBigCategoryName = 4  // 상권업종대분류명
	colMidCategoryCode = 5  // 상권업종중분류코드
	colMidCategoryName = 6  // 상권업종중분류명
	colSmlCategoryCode = 7  // 상권업종소분류코드 (6자리 영숫자, places.category_code)
	colSmlCategoryName = 8  // 상권업종소분류명
	colStdIndustryCode = 9  // 표준산업분류코드
	colStdIndustryName = 10 // 표준산업분류명
	colSidoCode        = 11 // 시도코드
	colSidoName        = 12 // 시도명
	colSigunguCode     = 13 // 시군구코드
	colSigunguName     = 14 // 시군구명
	colAdmDongCode     = 15 // 행정동코드
	colAdmDongName     = 16 // 행정동명
	colLegalDongCode   = 17 // 법정동코드
	colLegalDongName   = 18 // 법정동명
	colJibunCode       = 19 // 지번코드
	colLandKindCode    = 20 // 대지구분코드
	colLandKindName    = 21 // 대지구분명
	colJibunMain       = 22 // 지번본번지
	colJibunSub        = 23 // 지번부번지
	colJibunAddress    = 24 // 지번주소 (places.jibun_address)
	colRoadCode        = 25 // 도로명코드
	colRoadName        = 26 // 도로명
	colBuildingMain    = 27 // 건물본번지
	colBuildingSub     = 28 // 건물부번지
	colBuildingMgmtNo  = 29 // 건물관리번호
	colBuildingName    = 30 // 건물명
	colRoadAddress     = 31 // 도로명주소 (places.road_address)
	colOldZipcode      = 32 // 구우편번호
	colNewZipcode      = 33 // 신우편번호
	colDongInfo        = 34 // 동정보
	colFloorInfo       = 35 // 층정보
	colHoInfo          = 36 // 호정보
	colLongitude       = 37 // 경도 (WGS84, EPSG:4326)
	colLatitude        = 38 // 위도 (WGS84, EPSG:4326)
)

// ColumnNames는 39개 컬럼명을 순서대로 담는다. CSV 헤더 검증에 사용한다.
var ColumnNames = [ColumnCount]string{
	"상가업소번호", "상호명", "지점명",
	"상권업종대분류코드", "상권업종대분류명",
	"상권업종중분류코드", "상권업종중분류명",
	"상권업종소분류코드", "상권업종소분류명",
	"표준산업분류코드", "표준산업분류명",
	"시도코드", "시도명", "시군구코드", "시군구명",
	"행정동코드", "행정동명", "법정동코드", "법정동명",
	"지번코드", "대지구분코드", "대지구분명",
	"지번본번지", "지번부번지", "지번주소",
	"도로명코드", "도로명", "건물본번지", "건물부번지",
	"건물관리번호", "건물명", "도로명주소",
	"구우편번호", "신우편번호", "동정보", "층정보", "호정보",
	"경도", "위도",
}

// 한반도 경위도 bbox. 이 범위를 벗어난 좌표는 적재하지 않고 스킵한다.
// 근거: docs/dataspec/sbiz.md 좌표계 실측(경도 124~132E, 위도 33~39N).
const (
	LonMin = 124.0
	LonMax = 132.0
	LatMin = 33.0
	LatMax = 39.0
)

// SourceName은 provenance 컬럼(places.source)에 기록하는 출처 식별자다.
const SourceName = "sbiz"
