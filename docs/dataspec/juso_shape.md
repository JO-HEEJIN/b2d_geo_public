# 도로명주소 건물 도형 (juso.go.kr 연계 전체분) 실측 명세

확보일 2026-07-07, 기준일 2026-06-01 (파일명 `Total.JUSURB.20260601.*`).
위치: `data/juso_shape/건물도형_전체분_{시도명}.zip` — 17개 시도 전량 (~1.4GB).
출처: 행안부 주소기반산업지원서비스 연계신청 승인분 (2026-07-03 신청,
2026-07-07 승인 확인, Birth2Death LLC).

## 좌표계 (실측 확정 — SDP R-04 / TC-I03 해소)

**.prj 미제공.** shp 헤더 바운딩박스 실측으로 판별:
서울 x 935,298~972,009 / y 1,936,891~1,966,802 → **EPSG:5179 (UTM-K, GRS80,
원점 127.5E, FN 2,000,000)** 범위와 정확 일치. 적재 시
`ST_SetSRID(geom, 5179)` 후 `ST_Transform(→4326)`.

## zip 구성 (시도당 3레이어 × shp/dbf/shx, .prj/.cpg 없음)

| 레이어 | 지오메트리 | 서울 레코드 | 내용 |
|---|---|---|---|
| `TL_SGCO_RNADR_MST` | polygon | 524,094 | 도로명주소 건물 도형 마스터 |
| `TL_SPBD_ENTRC` | point | 528,746 | 건물 출입구 |
| `TL_SPOT_CNTC` | (미실측) | — | 지점 연계 (추후 실측) |

## DBF 스키마 (서울 실측)

TL_SGCO_RNADR_MST (rec_len 75):

| 필드 | 타입 | 의미 (샘플) |
|---|---|---|
| ADR_MNG_NO | C26 | 주소관리번호 (111101044100268000001000) |
| SIG_CD | C5 | 시군구코드 (11110) |
| RN_CD | C7 | 도로명코드 (4100268) |
| BULD_SE_CD | C1 | 건물구분 (0) |
| BULD_MNNM | N5 | 건물본번 (1) |
| BULD_SLNO | N5 | 건물부번 (0) |
| BUL_MAN_NO | N7 | 건물관리번호 (0) |
| EQB_MAN_SN | N10 | 일련번호 (1983) |
| EFFECT_DE | C8 | 효력발생일 (20110729) |

TL_SPBD_ENTRC (rec_len 49):

| 필드 | 타입 | 의미 (샘플) |
|---|---|---|
| BUL_MAN_NO | N7 | 건물관리번호 — MST 조인 후보 키 (644) |
| ENTRC_SE | C2 | 출입구 구분 (RM) |
| ENT_MAN_NO | N10 | 출입구관리번호 (8612) |
| EQB_MAN_SN | N10 | 일련번호 |
| OPERT_DE | C14 | 처리일시 (20250206080139) |
| SIG_CD | C5 | 시군구코드 (11110) |

주의: 속성은 코드 위주(한글 없음). 도로명·건물명 텍스트는 한글DB
(`202605_도로명주소 한글_전체분.zip`, docs/dataspec/juso.md)와
ADR_MNG_NO/RN_CD로 결합하는 구조로 보임 — ETL 설계 시 조인 키 검증 필수.
별도 승인 품목 "출입구 정보"(data/juso_entrance/ 예정)와 TL_SPBD_ENTRC의
중복 여부는 후자 확보 후 대조.

## 부록: 변동자료 연계서비스(ADS) 스펙 (가이드 PDF + 클라이언트 소스 해부, 2026-07-07)

- 엔드포인트: `GET http://update.juso.go.kr/updateInfo.do?cntc_cd=&app_key=&date_gb=&retry_in=&req_dt=&req_dt2=`
  (재반영: retryInfo.do). **응답은 body가 아니라 HTTP 헤더** (err_code,
  approval_yn, total_count, normal_count, retry_count 등) — curl은 `-D -` 필요
- 자료구분코드(cntc_cd): 100001 도로명주소한글(일) / 200001 출입구정보(일) /
  200002 기초번호(일) / 300001 건물도형(일) / 300003 도로도형(일) /
  300006 구역의도형(월) 외 — 전체표는 가이드 PDF (data/ADSClient_DLL.zip 내)
- 현 보유 키(JUSO_API_KEY)는 ADS 미승인 (실측: err_code E0001, approval_yn N)
  — ADS는 "변동자료 연계서비스 신청서"로 별도 신청. 전체분 확보 완료 상태라
  운영 자동화 단계(Phase 6)에서 신청 예정
