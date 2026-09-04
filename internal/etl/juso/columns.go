package juso

// 도로명주소 한글DB 컬럼 명세. 근거: docs/dataspec/juso.md §2.1 (juso.go.kr 공식
// 활용가이드 rnadr_korDBGuide.pdf verbatim).
//
// 파일 공통 규격 (docs/dataspec/juso.md §1):
//   - 인코딩: MS949 (=CP949). golang.org/x/text/encoding/korean.EUCKR로 디코딩.
//   - 구분자: 파이프 '|'. 헤더 행 없음.
//   - 전체분은 전국 단일 zip(월 1개) 내부에 시도별 txt로 분할 제공.

// SourceName은 provenance 컬럼(buildings.source)에 기록하는 출처 식별자다.
const SourceName = "juso"

// Delimiter는 컬럼 구분자다. docs/dataspec/juso.md §1 verbatim("파이프('|') 이용").
const Delimiter = '|'

// --- 도로명주소 한글 (전체분/변동분), 24 컬럼 ---

// RnaddrkorColumnCount는 도로명주소 한글 한 행의 컬럼 수다. 파싱 시 검증에 쓴다.
const RnaddrkorColumnCount = 24

// 컬럼 인덱스(0-based). docs/dataspec/juso.md §2.1(1)의 1-based 순번과 대응한다.
const (
	rkMgmtNo          = 0  // 도로명주소관리번호 (26, PK1)
	rkLegalDongCode   = 1  // 법정동코드 (10)
	rkSidoName        = 2  // 시도명 (40)
	rkSigunguName     = 3  // 시군구명 (40)
	rkLegalEmdName    = 4  // 법정읍면동명 (40)
	rkLegalRiName     = 5  // 법정리명 (40)
	rkMountainYN      = 6  // 산여부 (1) 0:대지, 1:산
	rkJibunMain       = 7  // 지번본번(번지) (4, 숫자)
	rkJibunSub        = 8  // 지번부번(호) (4, 숫자)
	rkRoadCode        = 9  // 도로명코드 (12, PK2) 시군구코드(5)+도로명번호(7)
	rkRoadName        = 10 // 도로명 (80)
	rkUndergroundYN   = 11 // 지하여부 (1, PK3) 0:지상, 1:지하, 2:공중, 3:수상
	rkBuildingMain    = 12 // 건물본번 (5, 숫자, PK4)
	rkBuildingSub     = 13 // 건물부번 (5, 숫자, PK5)
	rkAdmDongCode     = 14 // 행정동코드 (60) 참고용
	rkAdmDongName     = 15 // 행정동명 (60) 참고용
	rkZoneNo          = 16 // 기초구역번호 (5) 우편번호
	rkPrevRoadAddr    = 17 // 이전도로명주소 (400)
	rkEffectiveDate   = 18 // 효력발생일 (8)
	rkApartmentClass  = 19 // 공동주택구분 (1)
	rkChangeReason    = 20 // 이동사유코드 (2) 31:신규, 34:수정, 63:폐지
	rkLedgerBuildName = 21 // 건축물대장건물명 (400)
	rkSigunguBuildNm  = 22 // 시군구용건물명 (400)
	rkNote            = 23 // 비고 (200)
)

// --- 관련지번, 14 컬럼 ---

// JibunColumnCount는 관련지번 한 행의 컬럼 수다.
const JibunColumnCount = 14

// 컬럼 인덱스(0-based). docs/dataspec/juso.md §2.1(2).
const (
	jbMgmtNo        = 0  // 도로명주소관리번호 (26, PK1)
	jbLegalDongCode = 1  // 법정동코드 (10, PK2)
	jbSidoName      = 2  // 시도명 (40)
	jbSigunguName   = 3  // 시군구명 (40)
	jbLegalEmdName  = 4  // 법정읍면동명 (40)
	jbLegalRiName   = 5  // 법정리명 (40)
	jbMountainYN    = 6  // 산여부 (1, PK3) 0:대지, 1:산
	jbJibunMain     = 7  // 지번본번(번지) (4, 숫자, PK4)
	jbJibunSub      = 8  // 지번부번(호) (4, 숫자, PK5)
	jbRoadCode      = 9  // 도로명코드 (12)
	jbUndergroundYN = 10 // 지하여부 (1)
	jbBuildingMain  = 11 // 건물본번 (5, 숫자)
	jbBuildingSub   = 12 // 건물부번 (5, 숫자)
	jbChangeReason  = 13 // 이동사유코드 (2)
)

// 이동사유코드. 변동분 갱신 시 사용 (docs/dataspec/juso.md §2.1 참고사항).
const (
	ChangeInsert = "31" // 신규 (INSERT)
	ChangeUpdate = "34" // 수정 (UPDATE)
	ChangeDelete = "63" // 폐지 (DELETE)
)
