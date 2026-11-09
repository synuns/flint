// approve 커맨드 — 검수 게이트 통과(§8: 승인 = 대화로 → 에이전트가 실행).
// `flint approve --id N` — pending→active 승격(T17). 성공 {"status":"approved","id":N}
// exit 0, 없는 id는 not_found 한 줄 JSON exit 1.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

// approveFn — seam. 기본 T17 실구현(자체 Repo open — Store 인터페이스에
// 갱신 경로가 없어 backup과 동일 방식), 테스트는 fake 교체(SUPERLITE_REAL=1은 위임).
var approveFn = func(ctx context.Context, id int) error {
	repo, err := db.NewRepo(ctx, dsnFromEnv())
	if err != nil {
		return err
	}
	defer repo.Close()
	return repo.Approve(ctx, id)
}

// runApprove — 승인 실행. st는 root 디스패치 호환용이며 사용하지 않는다.
func runApprove(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	id := -1
	supersede := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id", "--supersede":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("%s requires a number", flag)))
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, errLine(fmt.Errorf("%s: invalid id %q", flag, args[i])))
				return 1
			}
			if flag == "--id" {
				id = n
			} else {
				supersede = n
			}
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}
	if id < 1 {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("approve requires --id")))
		return 1
	}
	if supersede == id {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("--supersede must differ from --id")))
		return 1
	}

	if err := approveFn(ctx, id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			fmt.Fprintln(stdout, `{"error":"not_found","id":`+strconv.Itoa(id)+`}`)
			return 1
		}
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	// v0.29.0 R2: 승격과 동시 대체 — approve --id N --supersede OLD는
	// OLD(구 활성 지식)를 N으로 무효화·선수 관계 기록까지 한 번에
	// (maintain --supersede 갱신 경로와 동일 UPDATE).
	if supersede > 0 {
		if st == nil {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("--supersede requires store")))
			return 1
		}
		if serr := supersedeAfterApprove(ctx, st, supersede, id); serr != nil {
			fmt.Fprintln(stdout, `{"status":"approved","id":`+strconv.Itoa(id)+
				`,"supersede_error":`+quoteJSONApprove(serr.Error())+`}`)
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

func init() { Register("approve", runApprove) }

// supersedeAfterApprove — 승격 직후 OLD를 NEW로 대체 무효화(maintain
// --supersede 갱신 UPDATE 승계 — v0.29.0 R2). 리뷰 1: 커밋은 Store.Commit
// (dolt_add -A + dolt_commit — dolt_commit은 테이블이 아닌 내장 함수).
// 리뷰 2: OLD active 사전 확인(maintainSupersede 계약 정렬 — rows-affected
// 무시 무음 성공 금지).
func supersedeAfterApprove(ctx context.Context, st Store, oldID, newID int) error {
	rows, err := queryOnce(ctx, st,
		`SELECT status FROM knowledge WHERE id = $1`, []any{oldID})
	if err != nil {
		return err
	}
	if len(rows) == 0 || rows[0][0] == nil || fmt.Sprint(rows[0][0]) != "active" {
		return fmt.Errorf("supersede: id=%d가 active 아님", oldID)
	}
	if _, err := queryOnce(ctx, st, `
		UPDATE knowledge
		SET status = 'invalidated',
		    superseded_by = $2,
		    invalid_at = (SELECT valid_at FROM knowledge WHERE id = $2)
		WHERE id = $1 AND status = 'active' AND invalid_at IS NULL`,
		[]any{oldID, newID}); err != nil {
		return err
	}
	_, err = st.Commit(ctx, fmt.Sprintf(
		"approve+supersede: id=%d superseded=%d (승격 병합 경로) [session:0]", newID, oldID))
	return err
}

// similarActiveIDs — 승격된 지식과 유사한 active id들(v0.29.0 R2).
// 리뷰 3·5: 유사 근사는 topicKey(세션 공통 접두 제거 후 12룬 접두)의
// 단일 Go 구현으로 pending 판정과 공유 — SQL 이중 구현 드리프트 방지.
func similarActiveIDs(ctx context.Context, st Store, id int) []int {
	rows, err := queryOnce(ctx, st,
		`SELECT topic FROM knowledge WHERE id = $1`, []any{id})
	if err != nil || len(rows) == 0 || rows[0][0] == nil {
		return nil
	}
	key := topicSimilarityKey(fmt.Sprint(rows[0][0]))
	actives, err := queryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL AND id != $1`,
		[]any{id})
	if err != nil {
		return nil
	}
	return matchTopicPrefix(key, actives)
}

// quoteJSONApprove — 에러 문자열 JSON 리터럴.
func quoteJSONApprove(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
