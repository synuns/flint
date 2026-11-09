// compact.go — flint compact: PostCompact 훅 진입. 훅 JSON의 compaction
// 요약을 session 1행으로 자동 적립(--source hook 고정)하고 증류 대기
// 마커(T71)를 남긴다. 어떤 실패에도 exit 0·stdout 빈 줄(§13 비간섭).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// compactHook — PostCompact 훅이 stdin으로 넘기는 JSON 스키마.
type compactHook struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	CompactSummary string `json:"compact_summary"`
}

// runCompact — stdin 훅 JSON 파싱 → 요약 비빈이면 runWrite(session,
// --source hook)로 적립 → 성공+session_id 비빈이면 WriteMarker. 파싱 실패·
// 빈 요약·st nil은 무동작이며 반환은 언제나 0, stdout에는 절대 쓰지 않는다.
func runCompact(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if st == nil {
		return 0
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	var h compactHook
	if err := json.Unmarshal(b, &h); err != nil {
		return 0
	}
	if strings.TrimSpace(h.CompactSummary) == "" {
		return 0
	}
	// 적립 payload: 요약 1건을 세션 행으로 정규화해 전달(플랜 T72 계약).
	// runWrite(readPayloads)는 줄 단위 JSON 객체(JSONL)만 받으므로 배열이
	// 아니라 객체 1행으로 직렬화한다(배열 전달 시 파싱 실패로 무동작).
	payload, err := json.Marshal(map[string]any{
		"summary": h.CompactSummary,
		"agent":   "claude",
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0
	}
	code := runWrite(ctx, st, []string{"session", "--source", "hook"},
		strings.NewReader(string(payload)), io.Discard)
	if code != 0 {
		return 0
	}
	// 적립 성공 + 세션 식별 가능 → Stop 훅 nudge용 마커. 실패는 stderr
	// 1행 경고로 조용히(done 패턴, 비간섭 §13). v0.27.2 리뷰 1: 세션
	// 지식화 마커보다 먼저 — maybeSessionKnowledge가 적립하면 그 마커가
	// 이 파일을 덮어쓰고(같은 markerKindDistill), 스킵(짧음·중복·빈도)
	// 시 이 compact 마커가 남는다. 어느 쪽이든 증류 nudge는 유지된다.
	if h.SessionID != "" {
		if err := WriteMarker(h.SessionID, "compact"); err != nil {
			fmt.Fprintf(os.Stderr, "[flint] compact marker: %v\n", err)
		}
	}
	// v0.27.2: 세션 지식화(R1)의 표준 경로 — Claude Code 규격상
	// compact_summary를 실제로 전달하는 훅은 PostCompact뿐(Stop stdin에는
	// 이 필드가 없다). 세션 행(계측 원천)과 별개로 pending 지식 후보를
	// 적립한다 — 빈도 상한·dedup는 maybeSessionKnowledge가 지닌다.
	maybeSessionKnowledge(ctx, st, h.CompactSummary, "claude", h.SessionID, 0) // 0=폴백 조회(done과 달리 세션 행 id를 이미 알지 못함)
	return 0
}

func init() {
	Register("compact", runCompact)
}
