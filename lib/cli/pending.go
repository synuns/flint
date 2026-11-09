// pending.go — v0.29.0 R1: pending 지식 심사 뷰. 사람 게이트(승격)의
// 병목 해소 — 규칙 기반 삼단 추천(승격/병합/만료/보류)으로 검수 안내.
// 판정은 안내일 뿐 실행은 approve·maintain이 담당(신뢰 게이트 불변 —
// LLM 판정 배제 §13). 출력은 사람 읽는 테이블(read 형식 승계).
package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/synuns/flint/lib/store"
)

func init() { Register("pending", runPending) }

// pendingItem — 심사 행.
type pendingItem struct {
	ID                     int
	Topic, Summary, Origin string
	ValidAt                time.Time
}

// pendingVerdict — 규칙 기반 판정. 우선순위: 만료 > 병합 > 승격 > 보류.
type pendingVerdict struct {
	Kind string // promote | merge | expire | hold
	Why  string
}

// runPending — `flint pending` — 보류 지식 목록 + 추천.
func runPending(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s — 사용법: flint pending", args[0])))
		return 1
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	th := writeThresholds(ctx, st)
	rows, err := queryOnce(ctx, st, `
		SELECT id, topic, summary, origin, valid_at
		FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at, id`, nil)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	now := time.Now().UTC()
	counts := map[string]int{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		it := pendingItem{Topic: fmt.Sprint(r[1]), Summary: fmt.Sprint(r[2]), Origin: fmt.Sprint(r[3])}
		fmt.Sscan(fmt.Sprint(r[0]), &it.ID)
		if ts, ok := r[4].(time.Time); ok {
			it.ValidAt = ts.UTC()
		}
		v := verdictFor(ctx, st, it, th, now)
		counts[v.Kind]++
		fmt.Fprintf(stdout, "%d|%s|%d일|%d자|%s|%s\n",
			it.ID, verdictLabel(v.Kind), ageDays(now, it.ValidAt), summaryLen(it.Summary), originLabel(it.Origin), oneLine(it.Topic, 48))
		if v.Why != "" {
			fmt.Fprintf(stdout, "  ↳ %s\n", oneLine(v.Why, 70))
		}
	}
	total := counts["promote"] + counts["merge"] + counts["expire"] + counts["hold"]
	if total == 0 {
		fmt.Fprintln(stdout, `{"status":"ok","pending":0,"next":"세션 지식화·import가 다시 쌓는다"}`)
		return 0
	}
	fmt.Fprintf(stdout, "{\"status\":\"ok\",\"pending\":%d,\"promote\":%d,\"merge\":%d,\"expire\":%d,\"hold\":%d}\n",
		total, counts["promote"], counts["merge"], counts["expire"], counts["hold"])
	return 0
}

// verdictFor — 판정 규칙(우선순위 고정):
//   - 만료: valid_at 경과 > PendingAgeDays(doctor 기준 승계)
//   - 병합: 유사 active 존재(topic 접두 일치 — approve R2와 동일 근사)
//   - 승격: 최근 3일 이내 + 본문 40룬 이상(유의미성 근사)
//   - 보류: 그 외(짧은 최근 행 등 — 더 쌓여서 판단)
func verdictFor(ctx context.Context, st Store, it pendingItem, th store.Thresholds, now time.Time) pendingVerdict {
	age := now.Sub(it.ValidAt)
	if d := th.PendingAgeDays; d > 0 && age > time.Duration(d)*24*time.Hour {
		return pendingVerdict{"expire", fmt.Sprintf("%d일 경과(기준 %d일) — 만료 검토: maintain --invalidate --id %d --reason review", int(age.Hours()/24), d, it.ID)}
	}
	// 병합 후보 — 유사 active 탐색(승격 대상의 topic과 접두 일치).
	if sims := similarByTopicPrefix(ctx, st, it.Topic, it.ID); len(sims) > 0 {
		return pendingVerdict{"merge", fmt.Sprintf("유사 active %d건(id %s) — approve --id %d --supersede OLD 검토",
			len(sims), joinInts(sims), it.ID)}
	}
	if age <= time.Duration(th.PendingPromoteDays)*24*time.Hour && len([]rune(it.Summary)) >= th.PendingPromoteMinRunes {
		return pendingVerdict{"promote", "최근 적립·본문 충분 — approve --id " + fmt.Sprint(it.ID)}
	}
	return pendingVerdict{"hold", "본문 짧음 또는 관측 중 — 다음 검수까지 보류"}
}

// similarByTopicPrefix — topic 12룬 접두 일치 active id들(공유 Go 구현).
func similarByTopicPrefix(ctx context.Context, st Store, topic string, excludeID int) []int {
	actives, err := queryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL AND id != $1`,
		[]any{excludeID})
	if err != nil {
		return nil
	}
	out := matchTopicPrefix(topicSimilarityKey(topic), actives)
	sort.Ints(out)
	return out
}

// topicSimilarityKey — 유사 판정 기준 키. 리뷰 3: 세션 지식 topic은 전부
// "세션[agent] " 공통 접두를 공유해 12룬 접두 비교가 agent 라벨+첫 1룬으로
// 퇴화 — 세션 접두는 제거한 본문 키로 비교한다.
func topicSimilarityKey(topic string) string {
	if i := strings.Index(topic, "] "); strings.HasPrefix(topic, "세션[") && i > 0 {
		return topic[i+2:]
	}
	return topic
}

// matchTopicPrefix — key 12룬 접두가 일치하는 active (id,topic) 행의 id.
// approve·pending 양쪽이 공유하는 단일 근사 구현(리뷰 5).
func matchTopicPrefix(key string, actives [][]any) []int {
	prefix := truncateRunesImport(key, 12)
	if prefix == "" {
		return nil
	}
	var out []int
	for _, r := range actives {
		if len(r) < 2 || r[0] == nil {
			continue
		}
		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(r[0]), &id); n == 1 &&
			truncateRunesImport(topicSimilarityKey(fmt.Sprint(r[1])), 12) == prefix {
			out = append(out, id)
		}
	}
	return out
}

// originLabel — 출처 축약 표기(hook/agent/import:파일 — 리뷰 10 활용).
func originLabel(origin string) string {
	if strings.HasPrefix(origin, "import:") {
		return "import"
	}
	return origin
}

// verdictLabel — 판정 한글 표기.
func verdictLabel(kind string) string {
	switch kind {
	case "promote":
		return "승격"
	case "merge":
		return "병합"
	case "expire":
		return "만료"
	default:
		return "보류"
	}
}

// ageDays — 경과 일수(최소 0).
func ageDays(now, t time.Time) int {
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// summaryLen — 요약 룬 수.
func summaryLen(s string) int { return len([]rune(s)) }

// oneLine — 한 줄화(개행 제거·상한).
func oneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	return truncateRunesImport(strings.TrimSpace(s), max)
}

// joinInts — 콤마 결합(상한 5).
func joinInts(ids []int) string {
	if len(ids) > 5 {
		ids = ids[:5]
	}
	parts := make([]string, len(ids))
	for i, v := range ids {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}
