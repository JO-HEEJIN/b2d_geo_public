-- 002_buildings_juso_key.sql: buildings 자연키 확정 (001의 유보 항목 해소).
-- 근거: docs/dataspec/juso.md §2.1 — 도로명주소 한글DB의 안정적 유일키는
-- 도로명주소관리번호(26자, PK1). 멱등 적재와 향후 위치정보DB 좌표 조인에 사용한다.
-- 건물DB의 건물관리번호(25자)와는 다른 키다. 한글DB 적재이므로 도로명주소관리번호를 쓴다.

ALTER TABLE buildings ADD COLUMN juso_mgmt_no char(26);

-- juso 출처 레코드에 한해 도로명주소관리번호 유일성을 보장한다(부분 유니크).
-- 다른 출처(향후 건물DB 등)는 이 키가 없을 수 있으므로 NULL은 제외한다.
CREATE UNIQUE INDEX idx_buildings_juso_mgmt_no
    ON buildings (juso_mgmt_no)
    WHERE juso_mgmt_no IS NOT NULL;
