// Package migrations는 순수 SQL 마이그레이션 파일을 embed로 내장한다.
// 파일명 번호 순서로 적용되며, 적용된 파일은 수정하지 않고 신규 번호로 추가한다.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
