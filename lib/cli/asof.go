// asof — 시점 스펙 정규화(T81). 스펙 3종(ISO 시각·상대 Nh/Nd·해시 접두어)을
// dolt_log 조회로 커밋 해시 하나로 확정한다. 시간여행 질의는 항상 커밋 해시로
// 정규화(§3 — AS OF SYSTEM TIME 비의존). 무효 스펙은 ErrInvalidAsOf(CLI exit 3).
package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidAsOf — 무효 as-of 스펙(형식 오류·접두어 모호/미발견·첫 커밋 이전).
// 커맨드 계층에서 exit 3으로 매핑한다.
var ErrInvalidAsOf = errors.New("invalid as-of spec")

// asofMinPrefix — 해시 접두어 최소 길이(3자). 미만은 형식 오류.
const asofMinPrefix = 3

// ParseRelative — 상대 스펙 "Nh"·"Nd"를 현재 기준 과거 시각(Go 계산)으로
// 확장한다. d는 h*24 확장이며 정수가 아니면(2.5h·"2 days ago") ErrInvalidAsOf.
func ParseRelative(spec string) (time.Time, error) {
	fail := fmt.Errorf("무효 상대 as-of 스펙 %q (Nh·Nd 정수 형식만 허용): %w", spec, ErrInvalidAsOf)
	if len(spec) < 2 {
		return time.Time{}, fail
	}
	unit := spec[len(spec)-1]
	if unit != 'h' && unit != 'd' {
		return time.Time{}, fail
	}
	v, err := strconv.Atoi(spec[:len(spec)-1])
	if err != nil || v < 0 {
		return time.Time{}, fail
	}
	if unit == 'd' {
		v *= 24
	}
	return time.Now().Add(-time.Duration(v) * time.Hour), nil
}

// ParseAsOfSpec — 스펙을 커밋 해시로 정규화. 판정 순서: ① 상대(\d+[hd]는
// 문서화된 상대 형식이므로 우선) ② ISO(RFC3339·2006-01-02T15:04) ③ 16진
// 접두어(소문자, 3자 이상 — dolt 해시는 소문자 16진). ISO·상대는 시각 직전
// 최신 커밋, 접두어는 LIKE 일치 1건일 때만 확정(0건·2건 이상 = ErrInvalidAsOf).
func ParseAsOfSpec(ctx context.Context, st Store, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("빈 as-of 스펙: %w", ErrInvalidAsOf)
	}
	// ① 상대 스펙 — 형식이 아니면 다음 후보로 넘어간다.
	if t, err := ParseRelative(spec); err == nil {
		return commitAtOrBefore(ctx, st, spec, t)
	}
	// ② ISO 시각.
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, spec); err == nil {
			return commitAtOrBefore(ctx, st, spec, t)
		}
	}
	// ③ 해시 접두어 — $1 파라미터화로 접합 주입 방지(§3).
	if len(spec) >= asofMinPrefix && isHexPrefix(spec) {
		rows, err := queryOnce(ctx, st,
			`SELECT commit_hash FROM dolt_log WHERE commit_hash LIKE $1||'%' ORDER BY commit_order DESC`,
			[]any{spec})
		if err != nil {
			return "", err
		}
		switch len(rows) {
		case 1:
			if len(rows[0]) == 0 {
				return "", fmt.Errorf("dolt_log 응답 컬럼 부재: %w", ErrInvalidAsOf)
			}
			return fmt.Sprint(rows[0][0]), nil
		case 0:
			return "", fmt.Errorf("접두어 %q와 일치하는 커밋 없음: %w", spec, ErrInvalidAsOf)
		default:
			return "", fmt.Errorf("접두어 %q가 %d건 커밋과 일치(모호 — 더 길게): %w", spec, len(rows), ErrInvalidAsOf)
		}
	}
	return "", fmt.Errorf("무효 as-of 스펙 %q (ISO·Nh/Nd·해시 접두어 3자+): %w", spec, ErrInvalidAsOf)
}

// commitAtOrBefore — 시각 직전 최신 커밋 채택. date 비교는 Go 계산
// time.Time을 timestamptz 파라미터로 전달(v0.6 원칙 — SQL interval 금지).
func commitAtOrBefore(ctx context.Context, st Store, spec string, t time.Time) (string, error) {
	rows, err := queryOnce(ctx, st,
		`SELECT commit_hash FROM dolt_log WHERE date <= $1 ORDER BY commit_order DESC LIMIT 1`,
		[]any{t})
	if err != nil {
		return "", err
	}
	if len(rows) == 1 && len(rows[0]) > 0 {
		return fmt.Sprint(rows[0][0]), nil
	}
	// 0행 — 첫 커밋 이전 시점. 문구에 첫 커밋 시각 포함(C11 Snowflake 준용).
	first, err := queryOnce(ctx, st,
		`SELECT date FROM dolt_log ORDER BY commit_order ASC LIMIT 1`, nil)
	if err != nil {
		return "", err
	}
	if len(first) == 1 && len(first[0]) > 0 {
		return "", fmt.Errorf("그 시점(%s)엔 아직 커밋이 없습니다 (첫 커밋: %v): %w",
			spec, first[0][0], ErrInvalidAsOf)
	}
	return "", fmt.Errorf("커밋이 없는 저장소에는 as-of를 사용할 수 없습니다: %w", ErrInvalidAsOf)
}

// isHexPrefix — 소문자 16진 접두어 여부(빈 문자열 false).
func isHexPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
