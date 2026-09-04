package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// keyPrefixLen은 평문 저장을 대신해 노출하는 앞자리 길이다(식별용).
const keyPrefixLen = 8

// validScopes는 발급 가능한 scope 화이트리스트다. geo는 위치 API, realestate는
// /v1/appraisal/* 계열에 필요하다.
var validScopes = map[string]bool{"geo": true, "realestate": true}

// validTiers는 발급 가능한 요금 티어 화이트리스트다. demo는 무료 소량(월 캡 없음,
// rate limit만), standard는 유료 정액 1티어(월 호출 하드캡)다.
var validTiers = map[string]bool{"demo": true, "standard": true}

// ValidTier는 t가 발급 가능한 티어인지 확인한다.
func ValidTier(t string) bool { return validTiers[t] }

// HashKey는 평문 API 키의 SHA-256 해시를 hex로 돌려준다. DB에는 이 값만 저장한다.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// GenerateKey는 새 평문 키와 그 prefix(앞 8자), SHA-256 해시를 만든다.
// 평문은 호출자가 발급 시 1회만 노출하고 저장하지 않는다.
func GenerateKey() (plaintext, prefix, hash string, err error) {
	var b [24]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", "", err
	}
	plaintext = "b2d_" + hex.EncodeToString(b[:]) // "b2d_" + 48 hex = 52자
	prefix = plaintext[:keyPrefixLen]
	hash = HashKey(plaintext)
	return plaintext, prefix, hash, nil
}

// ParseScopes는 콤마 구분 scope 문자열을 검증·중복제거하여 슬라이스로 돌려준다.
func ParseScopes(csv string) ([]string, error) {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if !validScopes[s] {
			return nil, fmt.Errorf("unknown scope: %q (valid: geo, realestate)", s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one scope required")
	}
	return out, nil
}

// CreateAPIKey는 새 키를 발급해 해시만 저장하고, 발급된 평문과 prefix, id를 돌려준다.
// 평문은 반환 즉시 1회 노출 후 폐기하는 것이 호출자의 책임이다.
// monthlyCap 0은 월 캡 없음(rate limit만 적용)이다.
func CreateAPIKey(ctx context.Context, pool *pgxpool.Pool, name string, scopes []string, rateRPS int, tier string, monthlyCap int64) (id int64, plaintext, prefix string, err error) {
	if !ValidTier(tier) {
		return 0, "", "", fmt.Errorf("unknown tier: %q (valid: demo, standard)", tier)
	}
	if monthlyCap < 0 {
		return 0, "", "", fmt.Errorf("monthly cap must be >= 0")
	}
	plaintext, prefix, hash, err := GenerateKey()
	if err != nil {
		return 0, "", "", err
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO api_keys (key_hash, key_prefix, owner_name, scopes, rate_limit_rps, tier, monthly_cap)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		hash, prefix, name, scopes, rateRPS, tier, monthlyCap,
	).Scan(&id)
	if err != nil {
		return 0, "", "", err
	}
	return id, plaintext, prefix, nil
}
