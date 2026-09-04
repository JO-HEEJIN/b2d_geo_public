-- 001_init.sql: 스키마 초안 (02_UML ERD 기반)
-- 주의: buildings의 자연키(건물관리번호 등)는 Phase 1에서 juso 파일 명세 확인 후
-- 별도 마이그레이션으로 확정한다. 여기서는 ERD 구조만 반영한다.

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE buildings (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    road_address    text NOT NULL,
    jibun_address   text,
    building_name   text,
    bcode           char(10) NOT NULL,
    pnu             text,
    geom            geometry(Point, 4326),
    norm_road       text NOT NULL,
    norm_jibun      text,
    source          text NOT NULL,
    source_version  date NOT NULL
);

CREATE TABLE places (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    store_number    text NOT NULL UNIQUE,
    name            text NOT NULL,
    norm_name       text NOT NULL,
    category_code   char(6),
    road_address    text,
    jibun_address   text,
    geom            geometry(Point, 4326),
    source          text NOT NULL,
    source_version  date NOT NULL
);

CREATE TABLE parcels (
    pnu             text PRIMARY KEY,
    bcode           char(10) NOT NULL,
    jibun           text,
    geom            geometry(MultiPolygon, 4326),
    land_category   text,
    source          text NOT NULL,
    source_version  date NOT NULL
);

CREATE TABLE admin_areas (
    bcode           char(10) PRIMARY KEY,
    name            text NOT NULL,
    geom            geometry(MultiPolygon, 4326)
);

CREATE TABLE api_keys (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key_hash        text NOT NULL UNIQUE,
    key_prefix      text NOT NULL,
    owner_name      text NOT NULL,
    rate_limit_rps  int NOT NULL DEFAULT 10,
    created_at      timestamptz NOT NULL DEFAULT now(),
    revoked_at      timestamptz
);

CREATE TABLE usage_log (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    api_key_id      bigint NOT NULL REFERENCES api_keys(id),
    endpoint        text NOT NULL,
    count           int NOT NULL,
    bucket          timestamptz NOT NULL
);

-- 공간 인덱스 (GiST)
CREATE INDEX idx_buildings_geom ON buildings USING gist (geom);
CREATE INDEX idx_places_geom ON places USING gist (geom);
CREATE INDEX idx_parcels_geom ON parcels USING gist (geom);
CREATE INDEX idx_admin_areas_geom ON admin_areas USING gist (geom);

-- trigram 인덱스 (정규화 컬럼)
CREATE INDEX idx_buildings_norm_road_trgm ON buildings USING gin (norm_road gin_trgm_ops);
CREATE INDEX idx_buildings_norm_jibun_trgm ON buildings USING gin (norm_jibun gin_trgm_ops);
CREATE INDEX idx_places_norm_name_trgm ON places USING gin (norm_name gin_trgm_ops);

-- 사용량 집계 조회용
CREATE INDEX idx_usage_log_key_bucket ON usage_log (api_key_id, bucket);
