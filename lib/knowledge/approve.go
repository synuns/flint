// approve.go — approve 커맨드 — 검수 게이트 통과(§8: 승인 = 대화로 →
// 에이전트가 실행). `flint approve --id N` — pending→active 승격(T17).
// 성공 {"status":"approved","id":N} exit 0, 없는 id는 not_found 한 줄
// JSON exit 1.
// v0.39.0 T4: lib/cli에서 이동(cli/approve.go 원본. realStore.Approve
// 위임 메서드는 cli/realstore_curation.go로 추출).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// approveStore — Approve 승격 접근. wire.Store 인터페이스는 v0.1 소유라
// 확장 대신 타입 단언으로 소비한다(root.go 수정 금지 — done.go
// staleSessionStore 관례). v0.34.3 R14: 실경로(root·mcp)가 넘기는
// realStore는 항상 구현하며 자체 db.NewRepo 연결은 폐지됐다.
type approveStore interface {
	Approve(ctx context.Context, id int) error
}

// ApproveFn — 레거시 폴백 seam. st가 Approve를 구현하지 않는(주입 없는
// 테스트 harness) 경우에만 쓰인다 — 기본은 에러(실경로 st 위임 전제).
// 테스트는 fake 교체(approve_test.go).
var ApproveFn = func(ctx context.Context, id int) error {
	return errors.New("approve: 주입 store가 Approve를 구현하지 않음")
}

// quoteJSON — 경로 등 문자열을 JSON 문자열 리터럴로 감싼다.
// cli/backup.go 소유분과 동일 구현(knowledge는 cli를 임포트할 수
// 없다 — 사이클 금지. 순수 함수 복제, 향후 수렴 부채).
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// RunApprove — 승인 실행. 승격은 주어진 st 경유(R14 재배선 — 이전엔 st를
// 무시하고 자체 Repo를 열었다).
func RunApprove(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	id := -1
	supersede := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--supersede":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s requires a number", flag)))
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s: invalid id %q", flag, args[i])))
				return 1
			}
			if flag == "--id" {
				id = n
			} else {
				supersede = n
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}
	if id < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("approve requires --id")))
		return 1
	}
	if supersede == id {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("--supersede must differ from --id")))
		return 1
	}

	// v0.34.3 R14: 주입 st 우선 — realStore는 Approve를 위임한다. 미구현
	// st(nil 포함)만 레거시 ApproveFn seam으로 폴백.
	var err error
	if as, ok := st.(approveStore); ok {
		err = as.Approve(ctx, id)
	} else {
		err = ApproveFn(ctx, id)
	}
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fmt.Fprintln(stdout, `{"error":"not_found","id":`+strconv.Itoa(id)+`}`)
			return 1
		}
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// v0.29.0 R2: 승격과 동시 대체 — approve --id N --supersede OLD는
	// OLD(구 활성 지식)를 N으로 무효화·선수 관계 기록까지 한 번에
	// (maintain --supersede 갱신 경로와 동일 UPDATE).
	if supersede > 0 {
		if st == nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("--supersede requires store")))
			return 1
		}
		if serr := supersedeAfterApprove(ctx, st, supersede, id); serr != nil {
			fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
				`,"supersede_error":`+quoteJSON(serr.Error())+`}`)
			return 1
		}
		fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
			`,"superseded":`+strconv.Itoa(supersede)+`}`)
		return 0
	}
	// v0.29.0 R2: 유사 active 안내 — 같은 topic(또는 접두 일치)의 기존
	// active가 있으면 병합 후보로 노출(승격은 이미 완료 — 안내만).
	// st nil(테스트 harness)은 안내 생략 — 승격 계약 불변.
	var sim []int
	if st != nil {
		sim = similarActiveIDs(ctx, st, id)
	}
	if len(sim) > 0 {
		ids := ""
		for i, s := range sim {
			if i > 0 {
				ids += ","
			}
			ids += strconv.Itoa(s)
		}
		fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
			`,"similar_active":[`+ids+`],"hint":"병합 후보 — flint approve --id `+
			strconv.Itoa(id)+` --supersede OLD 또는 flint maintain --supersede OLD --by NEW"}`)
		return 0
	}
	fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+`}`)
	return 0
}

func init() { wire.Register("approve", RunApprove) }

// supersedeAfterApprove — 승격 직후 OLD를 NEW로 대체 무효화(maintain
// --supersede 갱신 UPDATE 승계 — v0.29.0 R2). 리뷰 1: 커밋은 Store.Commit
// (dolt_add -A + dolt_commit — dolt_commit은 테이블이 아닌 내장 함수).
// 리뷰 2: OLD active 사전 확인(maintainSupersede 계약 정렬 — rows-affected
// 무시 무음 성공 금지).
func supersedeAfterApprove(ctx context.Context, st wire.Store, oldID, newID int) error {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT status FROM knowledge WHERE id = $1`, []any{oldID})
	if err != nil {
		return err
	}
	if len(rows) == 0 || rows[0][0] == nil || fmt.Sprint(rows[0][0]) != "active" {
		return fmt.Errorf("supersede: id=%d가 active 아님", oldID)
	}
	if _, err := wire.QueryOnce(ctx, st, `
		UPDATE knowledge
		SET status = 'invalidated',
		    superseded_by = $2,
		    invalid_at = (SELECT valid_at FROM knowledge WHERE id = $2)
		WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`,
		[]any{oldID, newID}); err != nil {
		return err
	}
	// v0.31.3(감사 2): 후검사 — QueryOnce가 rows-affected를 폐기하므로
	// 사전 확인과 UPDATE 사이 경합(병렬 무효화)에서 0행 갱신이 무음
	// 성공으로 남던 창을 결과 확인으로 닫는다.
	rows, verr := wire.QueryOnce(ctx, st,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, []any{oldID})
	if verr != nil {
		return verr
	}
	if len(rows) == 0 || rows[0][0] == nil || fmt.Sprint(rows[0][0]) != fmt.Sprint(newID) {
		return fmt.Errorf("supersede: id=%d 갱신 미반영(경합 — 재시도)", oldID)
	}
	_, err = st.Commit(ctx, fmt.Sprintf(
		"approve+supersede: id=%d superseded=%d (승격 병합 경로) [session:0]", newID, oldID))
	return err
}

// similarActiveIDs — 승격된 지식과 유사한 active id들(v0.29.0 R2).
// 리뷰 3·5: 유사 근사는 topicSimilarityKey(세션 공통 접두 제거 후 12룬
// 접두)의 단일 Go 구현으로 pending 판정과 공유 — SQL 이중 구현 드리프트
// 방지.
func similarActiveIDs(ctx context.Context, st wire.Store, id int) []int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT topic FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 || rows[0][0] == nil {
		return nil
	}
	key := topicSimilarityKey(fmt.Sprint(rows[0][0]))
	actives, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL AND id != $1`,
		[]any{id})
	if err != nil {
		return nil
	}
	return matchTopicPrefix(key, actives)
}
