-- 010 — 초성 검색 (Phase 3): 상호명 초성 컬럼 + trigram 인덱스
ALTER TABLE places ADD COLUMN choseong text;
CREATE INDEX idx_places_choseong_trgm ON places USING gin (choseong gin_trgm_ops);
