// sync.go — `flint sync remote add|list|remove` CLI 진입.
// `flint sync push|pull [--dry]` 확장(충돌 시 수동 해결 안내).
// 쿼리 구현은 lib/db/sync.go(dolt_remote 래핑) 소유 — 이 파일은 인자
// 검증·exit 계약·출력 마스킹만 담는다(쓰기 SQL 리터럴의 db 소유 원칙,
// BumpDailyFlatKey 관례). Review Focus(security): 원격 url 원문은
// 성공·오류·사용법 어떤 stdout 행에도 실리지 않는다 — list의 url은
// db.MaskRemoteURL(스킴+호스트) 결과뿐, add 성공 행은 이름·상태만.
// usageOrder의 "sync" 항목과 정합 — Register는 여기서, usage 문구는
// wire.usageOrder 단일 소스. push/pull도 이 파일에 확장.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// syncUsage — 사용법 안내(exit 3 공용 문구 — url 예는 스킴+호스트
// 형태만, 실제 형식 안내에 자격증명 예시를 실지 않는다).
const syncUsage = "usage: flint sync remote add <name> <url> | flint sync remote list | flint sync remote remove <name> | flint sync push <remote> | flint sync pull <remote> [--dry]"

func init() { Register("sync", runSync) }

// runSync — sync 커맨드 진입(wire.Handler 시그니처). 계약:
//   - 사용법 오류(인자 수·무효 sub·빈 값·스킴 없는 url) exit 3
//   - 중복 add·미존재 remove exit 3(사용법류 오류 계약)
//   - 스토어 nil·서버다운 exit 2, 기타 실행 오류 exit 1(원인 보존)
//   - 성공은 결과 1행 JSON stdout, exit 0
func runSync(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	switch args[0] {
	case "remote":
		return runSyncRemote(ctx, st, args[1:], stdout)
	case "push":
		return runSyncPush(ctx, st, args[1:], stdout)
	case "pull":
		return runSyncPull(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
}

// runSyncRemote — `flint sync remote ...` 하위 명령 분기.
func runSyncRemote(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	switch args[0] {
	case "add":
		return runSyncRemoteAdd(ctx, st, args[1:], stdout)
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
			return 3
		}
		return runSyncRemoteList(ctx, st, stdout)
	case "remove":
		return runSyncRemoteRemove(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
}

// runSyncPush — `flint sync push <remote>`: dolt_push(remote,'main') 결과
// {remote,status,hash} JSON. 미등록 exit 3, 실행 오류 exit 1(마스킹).
func runSyncPush(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	remote := args[0]
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	res, err := db.SyncPush(ctx, st, remote)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": remote, "status": res.Status, "hash": res.Hash})
	return 0
}

// runSyncPull — `flint sync pull <remote> [--dry]`: dolt_fetch+dolt_merge.
// 충돌은 exit 1 + 수동 해결 안내(자동 해결 금지). --dry는 fetch까지만.
func runSyncPull(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 || len(args) > 2 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	dry := false
	if len(args) == 2 {
		if args[1] != "--dry" {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
			return 3
		}
		dry = true
	}
	remote := args[0]
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	res, err := db.SyncPull(ctx, st, remote, dry)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": remote, "status": res.Status, "hash": res.Hash})
	return 0
}

// syncStoreReady — 스토어 nil·서버다운 검사(export 관례 승계 — ErrLine
// 한 줄 + MapExit, exit 2).
func syncStoreReady(ctx context.Context, st Store, stdout io.Writer) bool {
	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return false
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return false
	}
	return true
}

// runSyncRemoteAdd — `flint sync remote add <name> <url>`. url은 스킴
// 필수(https:// · file:// · postgres:// — url.Parse 기준). 성공 행에
// url을 실지 않는다(이름·상태만).
func runSyncRemoteAdd(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	name, remoteURL := args[0], args[1]
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "name", Expect: "non-empty"}))
		return 3
	}
	// url 검증 — 스킴 부재·공백·파손은 사용법 오류(원문은 안내에 재기록
	// 하지 않는다 — 스킴 형식 안내만).
	if !validRemoteURL(remoteURL) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "url", Expect: "remote URL with scheme (https://, file://, postgres://)"}))
		return 3
	}
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	if err := db.RemoteAdd(ctx, st, name, remoteURL); err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": name, "status": "added"})
	return 0
}

// runSyncRemoteList — `flint sync remote list`. url은 마스킹만.
func runSyncRemoteList(ctx context.Context, st Store, stdout io.Writer) int {
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	remotes, err := db.RemoteList(ctx, st)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	out := make([]map[string]string, 0, len(remotes))
	for _, r := range remotes {
		out = append(out, map[string]string{"name": r.Name, "url": db.MaskRemoteURL(r.URL)})
	}
	syncLineJSON(stdout, map[string]any{"remotes": out})
	return 0
}

// runSyncRemoteRemove — `flint sync remote remove <name>`.
func runSyncRemoteRemove(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	name := args[0]
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "name", Expect: "non-empty"}))
		return 3
	}
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	if err := db.RemoteRemove(ctx, st, name); err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": name, "status": "removed"})
	return 0
}

// syncOpExit — 원격 연산 오류의 exit 매핑. 중복명·미존재명은 사용법류
// exit 3, 그 외 실행 오류는 exit 1(ErrLine 원인 보존 — 오류 체인에
// url 원문이 실리지 않음은 db 쪽 래핑 계약).
func syncOpExit(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrRemoteExists) || errors.Is(err, db.ErrRemoteNotFound) {
		return 3
	}
	return 1
}

// syncLineJSON — 결과 1행 JSON 출력(ErrLine 채널 관례 — stdout 1행).
func syncLineJSON(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return // 마샬 불가값 없음 — 사실상 도달 불가
	}
	fmt.Fprintln(w, string(b))
}

// validRemoteURL — add url 검증: 파싱 가능·스킴 존재·공백 부재.
// 마스킹과 동일 기준(net/url 파싱 + 제어문자 가드).
func validRemoteURL(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme != ""
}
