// util.go — 순수 함수 단일 소스. cellToTime(셀→time)·
// quoteJSON(JSON 문자열 리터럴)·backupEncryptOn(meta 판정) 소유.
// wire는 knowledge·ops 양쪽이 임포트하는 계약층 — 소비자는
// var 별칭 위임, 동작 불변.
package wire

import (
	"encoding/json"
	"time"
)

// CellToTime — 셀(any)→time 변환(pgx timestamptz=time.Time, fake·
// 문자열은 RFC3339). 매핑 불가칸은 (zero, false).
func CellToTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		p, err := time.Parse(time.RFC3339, t)
		return p, err == nil
	default:
		return time.Time{}, false
	}
}

// QuoteJSON — 경로 등 문자열을 JSON 문자열 리터럴로 감싼다(마샬 실패
// 시 빈 리터럴 폴백 — 동작 계약 승계).
func QuoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// BackupEncryptOn — meta backup_encrypt 값 판정의 단일 소스
// (knowledge·ops 소비자는 var 별칭 위임). "true"·"1"만 참.
func BackupEncryptOn(v string) bool {
	return v == "true" || v == "1"
}
