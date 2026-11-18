// asof — 시점 스펙 정규화. 스펙 3종(ISO 시각·상대 Nh/Nd·해시 접두어)을
// Store.LogEntries 조회로 커밋 해시 하나로 확정한다. 시간여행 질의는 항상 커밋 해시로
// 정규화(§3 — AS OF SYSTEM TIME 비의존). 무효 스펙은 ErrInvalidAsOf(CLI exit 3).
// 공용 Store 유틸 — knowledge·ops seam이 소비. cli는 별칭(cli/asof.go)으로
// 재노출한다.
package wire

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

// AsofMinPrefix — 해시 접두어 최소 길이(3자). 미만은 형식 오류.
const AsofMinPrefix = 3

// ParseRelative — 상대 스펙 "Nh"·"Nd"를 현재 기준 과거 시각(Go 계산)으로
// 확장한다. d는 h*24 확장이며 정수가 아니면(2.5h·"2 days ago") ErrInvalidAsOf.
func ParseRelative(spec string) (time.Time, error) {
	fail := fmt.Errorf("invalid relative as-of spec %q (only integer Nh·Nd form allowed): %w", spec, ErrInvalidAsOf)
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
// 문서화된 상대 형식이므로 우선) ② ISO(RFC3339·2006-01-02T15:04) ③ 해시
// 접두어(소문자 영숫자 3자 이상 — Doltgres 커밋 해시는 base32계 [0-9a-z]
// 전범위, 실측). ISO·상대는 시각 직전 최신 커밋, 접두어는 접두 일치
// 1건일 때만 확정(0건·2건 이상 = ErrInvalidAsOf — 유일성은 wire len 검사).
func ParseAsOfSpec(ctx context.Context, st Store, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf)
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
	// ③ 해시 접두어 — 소문자 영숫자 검증 후 LogQuery{Prefix}로 일치 전체
	// 조회(정렬 무관). 유일성(0건·2건 이상 = ErrInvalidAsOf)은 len 검사.
	if len(spec) >= AsofMinPrefix && isLowerAlnumPrefix(spec) {
		entries, err := st.LogEntries(ctx, LogQuery{Prefix: spec})
		if err != nil {
			return "", err
		}
		switch len(entries) {
		case 1:
			return entries[0].Hash, nil
		case 0:
			return "", fmt.Errorf("no commit matches prefix %q: %w", spec, ErrInvalidAsOf)
		default:
			return "", fmt.Errorf("prefix %q matches %d commits (ambiguous — use more characters): %w", spec, len(entries), ErrInvalidAsOf)
		}
	}
	return "", fmt.Errorf("invalid as-of spec %q (ISO datetime, Nh/Nd, or hash prefix 3+ chars): %w", spec, ErrInvalidAsOf)
}

// commitAtOrBefore — 시각 직전 최신 커밋 채택(LogQuery{BeforeOrAt}는
// date≤ 최신 1건). date 비교는 Go 계산 time.Time 그대로 전달.
func commitAtOrBefore(ctx context.Context, st Store, spec string, t time.Time) (string, error) {
	entries, err := st.LogEntries(ctx, LogQuery{BeforeOrAt: t})
	if err != nil {
		return "", err
	}
	if len(entries) == 1 {
		return entries[0].Hash, nil
	}
	// 0행 — 첫 커밋 이전 시점. 문구에 첫 커밋 시각 포함(C11 Snowflake 준용).
	first, err := st.LogEntries(ctx, LogQuery{Earliest: true})
	if err != nil {
		return "", err
	}
	if len(first) == 1 {
		return "", fmt.Errorf("no commits exist yet at %s (first commit: %v): %w",
			spec, first[0].Date, ErrInvalidAsOf)
	}
	return "", fmt.Errorf("as-of cannot be used on a repository with no commits: %w", ErrInvalidAsOf)
}

// isLowerAlnumPrefix — 소문자 영숫자 [0-9a-z] 접두어 여부(빈 문자열 false).
// Doltgres 커밋 해시가 base32계([0-9a-z] 전범위)라 16진 한정 검증은 실DB
// 해시를 전부 거부했다(실측 결함 폐쇄). 유일성 검증은 LogEntries 결과
// len 검사가 담당.
func isLowerAlnumPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}
