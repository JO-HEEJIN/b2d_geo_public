-- 007_appraisal.sql — 감정평가 사실조회 모듈 스키마 (Phase 7-0)
-- 감정평가 사실조회용 테이블, 인덱스, API scope.
-- 절대 경계: 사실 저장만. 보정/평가 파생 컬럼 없음 (price_per_sqm은 단순 나눗셈).

-- 표준지 (연도별 이력 보존)
CREATE TABLE standard_lots (
  pnu            char(19) NOT NULL,
  base_year      smallint NOT NULL,
  price_per_sqm  bigint   NOT NULL,
  land_category  text,
  use_zone       text,
  land_use       text,
  road_side      text,
  terrain_height text,
  terrain_shape  text,
  area_sqm       numeric,
  geom           geometry(Point, 4326) NOT NULL,
  source         text NOT NULL DEFAULT 'molit_std',
  source_version date NOT NULL,
  PRIMARY KEY (pnu, base_year)
);

-- 개별공시지가 (대용량: 연도 파티셔닝)
CREATE TABLE official_land_prices (
  pnu char(19) NOT NULL, base_year smallint NOT NULL,
  price_per_sqm bigint NOT NULL,
  source text NOT NULL DEFAULT 'molit_ind', source_version date NOT NULL,
  PRIMARY KEY (pnu, base_year)
) PARTITION BY LIST (base_year);
CREATE TABLE official_land_prices_default
  PARTITION OF official_land_prices DEFAULT;

-- 토지특성 (연도별)
CREATE TABLE land_features (
  pnu char(19) NOT NULL, base_year smallint NOT NULL,
  land_use text, terrain_height text, terrain_shape text, road_side text,
  source text NOT NULL DEFAULT 'molit_feat', source_version date NOT NULL,
  PRIMARY KEY (pnu, base_year)
);

-- 용도지역 폴리곤
CREATE TABLE use_zones (
  id bigserial PRIMARY KEY,
  zone_code text NOT NULL, zone_name text NOT NULL,
  geom geometry(MultiPolygon, 4326) NOT NULL,
  source text NOT NULL DEFAULT 'nsdi_upis', source_version date NOT NULL
);

-- 토지 실거래 (위치 정밀도 등급 필수)
CREATE TABLE land_sales (
  id bigserial PRIMARY KEY,
  emd_code char(10) NOT NULL,
  jibun_partial text,
  contract_date date NOT NULL,
  area_sqm numeric NOT NULL,
  price_total bigint NOT NULL,
  price_per_sqm bigint GENERATED ALWAYS AS
    (CASE WHEN area_sqm > 0 THEN (price_total / area_sqm)::bigint END) STORED,
  use_zone text, land_category text,
  geom geometry(Point, 4326),
  location_precision text NOT NULL
    CHECK (location_precision IN ('PARCEL','JIBUN_PARTIAL','EMD_CENTROID')),
  source text NOT NULL DEFAULT 'molit_rt', source_version date NOT NULL
);

-- 지가변동률
CREATE TABLE land_price_index (
  sigungu_code char(5) NOT NULL,
  use_zone_class text NOT NULL,
  month char(6) NOT NULL,
  rate_pct numeric NOT NULL,
  source text NOT NULL DEFAULT 'reb_rone', source_version date NOT NULL,
  PRIMARY KEY (sigungu_code, use_zone_class, month)
);

-- 인덱스 (지시서 지정)
CREATE INDEX idx_standard_lots_geom ON standard_lots USING gist (geom);
CREATE INDEX idx_use_zones_geom ON use_zones USING gist (geom);
CREATE INDEX idx_land_sales_geom ON land_sales USING gist (geom);
CREATE INDEX idx_standard_lots_zone_cat ON standard_lots (use_zone, land_category);
CREATE INDEX idx_land_sales_emd_date ON land_sales (emd_code, contract_date);

-- RealEstate scope (기본값: 전 scope)
ALTER TABLE api_keys ADD COLUMN scopes text[] NOT NULL DEFAULT '{geo,realestate}';

-- parcels 지번 조회 보조 인덱스 (by-jibun API)
CREATE INDEX idx_parcels_bcode_jibun ON parcels (bcode, jibun);
