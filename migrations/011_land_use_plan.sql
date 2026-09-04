-- 필지별 토지이용계획: 용도지역/지구/구역 + 포함/저촉 관계.
-- 출처: 국가공간정보포털(브이월드) 토지이용계획공간정보 AL_D154. 원자료의 사전판정
-- (포함/저촉)을 그대로 적재한다 — 우리가 ST_Area 임계값으로 재판정하지 않는다(사실 반환).
-- geom 미저장: land-use 엔드포인트는 PNU 조회만 필요. 저촉 면적비(%)는 원자료에 없음.
CREATE TABLE land_use_plan (
  pnu        char(19) NOT NULL,
  zone_code  text     NOT NULL,
  zone_name  text     NOT NULL,
  relation   text     NOT NULL,              -- 포함 | 저촉 (원자료 그대로)
  source     text     NOT NULL DEFAULT 'nsdi_luz',
  source_version date  NOT NULL,
  PRIMARY KEY (pnu, zone_code)
);
