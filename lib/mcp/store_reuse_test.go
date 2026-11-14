// store_reuse_test.go — v1.2.4 T1(B4): MCP 서버 수명 store 재사용 회귀.
// ① cli.RunWithStore 주입 검증(주입 fake의 Query 도달·Close 미호출 —
// 서버 수명 store가 첫 호출에 닫히지 않는다)
// ② 기존 cli.Run 동치(nil 주입 폴백 — store-free 경로·서버다운 exit 2)
// ③ DispatchTool 2회 호출 시 supply 1회(캐시)·동시 호출 경합·supply
// 실패 시 nil 폴백(조용히 기존 계약).
package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/wire"
)

// reuseFake — wire.Store fake. Query·Close 호출 수를 기록해 주입
// 경로 도달과 수명(닫힘 여부)을 검증한다.
type reuseFake struct {
	mu      sync.Mutex
	queries int
	closed  int
}

func (f *reuseFake) Migrate(ctx context.Context) error { return nil }
func (f *reuseFake) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *reuseFake) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *reuseFake) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *reuseFake) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *reuseFake) Ping(ctx context.Context) error { return nil }

func (f *reuseFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.mu.Lock()
	f.queries++
	f.mu.Unlock()
	return nil, nil
}

func (f *reuseFake) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

var _ wire.Store = (*reuseFake)(nil)

// resetStoreCache — 서버 수명 store 캐시 초기화(테스트 간 오염 차단).
func resetStoreCache(t *testing.T) {
	t.Helper()
	storeMu.Lock()
	cachedStore = nil
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		cachedStore = nil
		storeMu.Unlock()
	})
}

// installRunWithStore — runWithStoreFn seam 교체(호출 인자·주입 store
// 기록, 고정 출력·코드 반환).
func installRunWithStore(t *testing.T, out string, code int) *[]wire.Store {
	t.Helper()
	var got []wire.Store
	var mu sync.Mutex
	prev := runWithStoreFn
	runWithStoreFn = func(args []string, stdin io.Reader, stdout io.Writer, st wire.Store) int {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
		fmt.Fprint(stdout, out)
		return code
	}
	t.Cleanup(func() { runWithStoreFn = prev })
	return &got
}

// TestRunWithStoreReusesInjection — 주입 store가 커맨드 실행에 실제로
// 쓰이고(Query 도달) 실행 후에도 닫히지 않는다(wire.Run의 단발 Close
// 계약 차단 — 서버 수명 재사용의 핵심).
func TestRunWithStoreReusesInjection(t *testing.T) {
	f := &reuseFake{}
	var out bytes.Buffer
	code := cli.RunWithStore([]string{"read", "--source", "mcp"}, strings.NewReader(""), &out, f)
	f.mu.Lock()
	q, c := f.queries, f.closed
	f.mu.Unlock()
	if q == 0 {
		t.Fatalf("주입 store 미사용 — Query 도달 없음(exit=%d, out=%q)", code, out.String())
	}
	if c != 0 {
		t.Fatalf("주입 store Close 호출됨(%d회) — 서버 수명 위반", c)
	}
}

// TestRunDelegatesToRunWithStore — nil 주입은 기존 cli.Run과 동치
// (store-free 출력·exit·서버다운 exit 2 계약 불변).
func TestRunDelegatesToRunWithStore(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"help", []string{"help"}},
		{"no_args", nil},
		{"unknown", []string{"no-such-cmd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+"_equivalent", func(t *testing.T) {
			var a, b bytes.Buffer
			ca := cli.Run(tc.args, strings.NewReader(""), &a)
			cb := cli.RunWithStore(tc.args, strings.NewReader(""), &b, nil)
			if ca != cb {
				t.Fatalf("exit Run=%d RunWithStore(nil)=%d — 불일치", ca, cb)
			}
			if a.String() != b.String() {
				t.Fatalf("출력 불일치: Run=%q RunWithStore=%q", a.String(), b.String())
			}
		})
	}
	t.Run("server_down_exit2_both", func(t *testing.T) {
		// 접속 거부(포트 1)는 재시도 없이 ErrServerDown — 빠른 허메틱 시뮬.
		t.Setenv("FLINT_DSN", "postgres://flint:flint@127.0.0.1:1/flint?sslmode=disable")
		var a, b bytes.Buffer
		ca := cli.Run([]string{"hot"}, strings.NewReader(""), &a)
		cb := cli.RunWithStore([]string{"hot"}, strings.NewReader(""), &b, nil)
		if ca != 2 || cb != 2 {
			t.Fatalf("exit Run=%d RunWithStore=%d, want 2/2(server_down)", ca, cb)
		}
		if a.String() != b.String() {
			t.Fatalf("서버다운 출력 불일치: %q vs %q", a.String(), b.String())
		}
		if strings.TrimSpace(a.String()) != `{"error":"server_down"}` {
			t.Fatalf("server_down 한 줄 JSON 계약: %q", a.String())
		}
	})
}

// TestDispatchToolReusesServerStore — 도구 호출 2회에 supply(store open)
// 1회 — 서버 수명 캐시. 동시 호출 경합에서도 open 1회, supply 실패는
// nil 주입 폴백(조용히 기존 Run 계약).
func TestDispatchToolReusesServerStore(t *testing.T) {
	t.Run("two_calls_one_open", func(t *testing.T) {
		resetStoreCache(t)
		f := &reuseFake{}
		var opens int
		prevSupply := storeSupply
		storeSupply = func() (wire.Store, error) {
			opens++
			return f, nil
		}
		t.Cleanup(func() { storeSupply = prevSupply })
		got := installRunWithStore(t, `{"ok":true}`, 0)
		for i := 0; i < 2; i++ {
			out, code := DispatchTool("flint_hot", nil)
			if code != 0 || out != `{"ok":true}` {
				t.Fatalf("호출 %d: out=%q code=%d", i, out, code)
			}
		}
		if opens != 1 {
			t.Fatalf("supply(open) %d회, want 1 — 캐시 재사용 안 됨", opens)
		}
		if len(*got) != 2 || (*got)[0] != (*got)[1] {
			t.Fatalf("주입 store 불일치 — 같은 서버 수명 store여야 함 (len=%d)", len(*got))
		}
	})
	t.Run("concurrent_calls_single_open", func(t *testing.T) {
		resetStoreCache(t)
		f := &reuseFake{}
		var opens int32
		var opensMu sync.Mutex
		prevSupply := storeSupply
		storeSupply = func() (wire.Store, error) {
			opensMu.Lock()
			opens++
			opensMu.Unlock()
			return f, nil
		}
		t.Cleanup(func() { storeSupply = prevSupply })
		installRunWithStore(t, `{"ok":true}`, 0)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				DispatchTool("flint_hot", nil)
			}()
		}
		wg.Wait()
		if opens != 1 {
			t.Fatalf("동시 경합 시 open %d회, want 1(mutex 가드)", opens)
		}
	})
	t.Run("supply_failure_nil_fallback", func(t *testing.T) {
		resetStoreCache(t)
		prevSupply := storeSupply
		storeSupply = func() (wire.Store, error) { return nil, errors.New("db down") }
		t.Cleanup(func() { storeSupply = prevSupply })
		got := installRunWithStore(t, `{"ok":true}`, 0)
		out, code := DispatchTool("flint_hot", nil)
		if code != 0 || out != `{"ok":true}` {
			t.Fatalf("폴백 경로 미실행: out=%q code=%d", out, code)
		}
		if len(*got) != 1 || (*got)[0] != nil {
			t.Fatalf("supply 실패 시 nil 주입 폴백이어야 함 (got=%v)", *got)
		}
	})
}
