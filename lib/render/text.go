// text.go — 텍스트 출력 보조 유틸 단일 소스(v0.38.0 패키지 분할 1/3:
// lib/cli textutil.go의 truncateRunes와 pending.go의 countPending을
// 통합 이동, 스펙 §2). rune 절단 3중 복제 수렴(v0.34.3 감사 스윕)과
// pending 수 단일 조회 관례를 승계한다.
package render

import (
	"context"
	"strconv"

	"github.com/synuns/flint/lib/wire"
)

// TruncateRunes — rune 상한 절단(멀티바이트 안전). 상한 이하·빈 문자열은
// 불변 반환. 소비자 관례: import 본문 500룬·maintain 요약 접두 40룬·
// pending 한 줄화 상한(호출자 지정).
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// CountPending — pending 지식 수 단일 조회(v0.34.3: status·nudge·
// sessionhook의 3중 쿼리 복제 수렴 — report.go는 v0.36 트랙 제외).
// 조회 실패만 오류; 빈 응답·해석 불가 값은 0(기존 소비자 관례: 조용한
// 0 처리)이다. wire.Store 사용 — render→wire 의존 1방향(스펙 §2).
func CountPending(ctx context.Context, st wire.Store) (int, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`, nil)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, nil
	}
	n, _ := cellToInt(rows[0][0])
	return n, nil
}

// cellToInt — 셀(any)→int 강제 변환. lib/cli/read.go 구현과 동일 규칙
// (pgx 숫자형·float64·10진 문자열 수용, nil·비수는 0) — 이동 시점엔
// read.go 원본이 cli 잔류라 동일 본체를 둔다.
func cellToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}
