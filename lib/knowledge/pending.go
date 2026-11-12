// pending.go — v0.29.0 R1: pending 지식 심사 뷰. 사람 게이트(승격)의
// 병목 해소 — 규칙 기반 삼단 추천(승격/병합/만료/보류)으로 검수 안내.
// 판정은 안내일 뿐 실행은 approve·maintain이 담당(신뢰 게이트 불변 —
// LLM 판정 배제 §13). 출력은 사람 읽는 테이블(read 형식 승계).
// v0.39.0 T4: lib/cli에서 이동(cli/pending.go 원본 + lib/render/text.go
// 소유 CountPending — 룰링: render에는 임시 소속이었다).
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func init() { wire.Register("pending", RunPending) }

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

// CountPending — pending 지식 수 단일 조회(v0.34.3: status·nudge·
// sessionhook의 3중 쿼리 복제 수렴 — report.go는 v0.36 트랙 제외).
// 조회 실패만 오류; 빈 응답·해석 불가 값은 0(기존 소비자 관례: 조용한
// 0 처리)이다. v0.39.0 T4 룰링: lib/render에서 knowledge로 이동(정의는
// pending 도메인 소유 — render→knowledge 임포트는 사이클이라 불가).
func CountPending(ctx context.Context, st wire.Store) (int, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`, nil)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, nil
	}
	n, _ := render.CellToInt(rows[0][0])
	return n, nil
}

// RunPending — `flint pending` — 보류 지식 목록 + 추천.
// v1.0.1(F3): unknown flag는 무효 인자 exit 3(v0.36.1 maintain·import 관례
// 통일).
func RunPending(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true // v0.32.0: MCP·스크립트용 기계 판독 출력
			continue
		}
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("unknown flag: %s — 사용법: flint pending [--json]", a)))
		return 3
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	th := WriteThresholds(ctx, st)
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, origin, valid_at
		FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at, id`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	now := time.Now().UTC()
	// v0.31.3(감사 3): active 전체 1회 조회 — 행마다 재조회(O(P×A)) 제거.
	actives := fetchActiveTopicRows(ctx, st)
	counts := map[string]int{}
	// v0.32.0: JSON 모드용 행 구조 — 판정 데이터를 그대로 직렬화한다.
	type jsonItem struct {
		ID           int    `json:"id"`
		Verdict      string `json:"verdict"`
		AgeDays      int    `json:"age_days"`
		SummaryRunes int    `json:"summary_runes"`
		Origin       string `json:"origin"`
		Topic        string `json:"topic"`
		Why          string `json:"why,omitempty"`
	}
	jsonItems := make([]jsonItem, 0) // v0.32.0 리뷰 3: 빈 경우 null 아닌 []
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		// v0.31.3(감사 1): 저장값 출력 재살균 — v0.28 H③ 패턴(read·hot·
		// maintain 계열) 회귀 수선. 제어 문자·Cf·[flint] 마커 중립화.
		// v0.37.0: render 조크포인트 경유(render.Text).
		it := pendingItem{
			Topic:   render.Text(fmt.Sprint(r[1])),
			Summary: render.Text(fmt.Sprint(r[2])),
			Origin:  render.Text(fmt.Sprint(r[3])),
		}
		fmt.Sscan(fmt.Sprint(r[0]), &it.ID)
		if ts, ok := cellToTime(r[4]); ok { // v0.31.3(감사 4): 타입 단언 폴백 정경로
			it.ValidAt = ts.UTC()
		}
		// v0.32.1(감사 1·2): valid_at 파싱 불가 가드를 판정·counts 앞으로 —
		// jsonMode 한정 오버라이드가 counts(원 kind)와 items(보정 kind)를
		// 어긋나게 하던 모순 수선, 텍스트 모드에도 동일 적용.
		v := verdictFor(it, th, now, actives)
		if it.ValidAt.IsZero() {
			v = pendingVerdict{"hold", "valid_at 파싱 불가 — 데이터 확인 필요"}
		}
		counts[v.Kind]++
		// v0.33.0(패치 감사 1): zero valid_at의 age 표기 0 보정 복원 —
		// v0.32.1 가드 이동 시 유실돼 292년치 나이가 출력되던 회귀 수선.
		age := 0
		if !it.ValidAt.IsZero() {
			age = ageDays(now, it.ValidAt)
		}
		if jsonMode {
			jsonItems = append(jsonItems, jsonItem{
				ID: it.ID, Verdict: v.Kind, AgeDays: age,
				SummaryRunes: summaryLen(it.Summary), Origin: originLabel(it.Origin),
				Topic: OneLine(it.Topic, 80), Why: OneLine(v.Why, 120),
			})
		} else {
			fmt.Fprintf(stdout, "%d|%s|%d일|%d자|%s|%s\n",
				it.ID, verdictLabel(v.Kind), age, summaryLen(it.Summary), originLabel(it.Origin), OneLine(it.Topic, 48))
			if v.Why != "" {
				fmt.Fprintf(stdout, "  ↳ %s\n", OneLine(v.Why, 70))
			}
		}
	}
	if jsonMode {
		b, err := json.Marshal(map[string]any{
			"status": "ok",
			"items":  jsonItems,
			"counts": map[string]int{
				"pending": len(jsonItems), "promote": counts["promote"],
				"merge": counts["merge"], "expire": counts["expire"], "hold": counts["hold"],
			},
			"next": "approve --id N [--supersede OLD] · maintain --invalidate --id N --reason ...",
		})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
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
func verdictFor(it pendingItem, th store.Thresholds, now time.Time, actives [][]any) pendingVerdict {
	age := now.Sub(it.ValidAt)
	if d := th.PendingAgeDays; d > 0 && age > time.Duration(d)*24*time.Hour {
		return pendingVerdict{"expire", fmt.Sprintf("%d일 경과(기준 %d일) — 만료 검토: maintain --invalidate --id %d --reason review", int(age.Hours()/24), d, it.ID)}
	}
	// 병합 후보 — 유사 active 탐색(사전 조회 세트에서 접두 일치).
	if sims := matchTopicPrefix(topicSimilarityKey(it.Topic), actives); len(sims) > 0 {
		return pendingVerdict{"merge", fmt.Sprintf("유사 active %d건(id %s) — approve --id %d --supersede OLD 검토",
			len(sims), joinInts(sims), it.ID)}
	}
	if age <= time.Duration(th.PendingPromoteDays)*24*time.Hour && len([]rune(it.Summary)) >= th.PendingPromoteMinRunes {
		return pendingVerdict{"promote", "최근 적립·본문 충분 — approve --id " + fmt.Sprint(it.ID)}
	}
	return pendingVerdict{"hold", "본문 짧음 또는 관측 중 — 다음 검수까지 보류"}
}

// fetchActiveTopicRows — 유사 판정용 active (id, topic) 전체 1회 조회
// (v0.31.3 감사 3 — 행당 재조회 제거. 실패는 빈 세트: 병합 판정만
// 생략되고 나머지 판정·출력 계약 불변).
func fetchActiveTopicRows(ctx context.Context, st wire.Store) [][]any {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return nil
	}
	return rows
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
	prefix := render.TruncateRunes(key, 12)
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
			render.TruncateRunes(topicSimilarityKey(fmt.Sprint(r[1])), 12) == prefix {
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

// OneLine — 한 줄화(개행 제거·상한 — 절단은 단일 구현 TruncateRunes 위임).
// v0.39.0 T4 내보내기: cli 잔류 소비자(textutil_test의 관례 위임 동치
// 검증)가 knowledge.OneLine 별칭으로 무편집 컴파일된다.
func OneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	return render.TruncateRunes(strings.TrimSpace(s), max)
}

// cellToTime — 셀(any)→time 변환. v0.41.0 T4: 순수 함수 복제 폐지 —
// wire.CellToTime 단일 구현 별칭(사이클 금지 해소, 동일 계약).
var cellToTime = wire.CellToTime

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
