-- 셀프서브 티어: 키에 요금 티어와 월 호출 하드캡을 부여한다.
-- tier: 'demo'(무료 소량, 기존 정책) | 'standard'(유료 정액 1티어).
-- monthly_cap: 해당 월(UTC date_trunc 기준) 총 호출 상한. 0 = 무제한(기존 키 전부).
ALTER TABLE api_keys ADD COLUMN tier text NOT NULL DEFAULT 'demo';
ALTER TABLE api_keys ADD COLUMN monthly_cap bigint NOT NULL DEFAULT 0;
