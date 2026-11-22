// index_test.go — v1.21.0 T3: the `flint index` CLI contract (fast fakes).
// The scan producer (vault.Scan) is consumed through the indexScanStoreFn
// seam — a fake vault.Store over a synthetic demo vault by default, the real
// vault.Open connection when SUPERLITE_REAL=1 (the real-DB slow e2e is
// TestIndexE2ERealSlow). No personal vault paths appear anywhere in this
// file (the §3 privacy contract — synthetic demo data only).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/vault"
)

// fakeVaultStore — an in-memory vault.Store (the cli-side test seam over the
// real vault.Scan algorithm: Scan runs for real against this fake snapshot).
type fakeVaultStore struct {
	mu      sync.Mutex
	rows    map[string]vault.File
	applies int
	upserts []vault.File
	keeps   []string
}

func (f *fakeVaultStore) Existing(ctx context.Context) (map[string]vault.Existing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]vault.Existing, len(f.rows))
	for p, r := range f.rows {
		out[p] = vault.Existing{Mtime: r.Mtime, Size: r.Size, ContentHash: r.ContentHash}
	}
	return out, nil
}

func (f *fakeVaultStore) ApplyDiff(ctx context.Context, upserts []vault.File, keepPaths []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies++
	f.upserts = append(f.upserts, upserts...)
	f.keeps = keepPaths
	keep := make(map[string]bool, len(keepPaths))
	for _, p := range keepPaths {
		keep[p] = true
	}
	var removed int64
	for p := range f.rows {
		if !keep[p] {
			delete(f.rows, p)
			removed++
		}
	}
	for _, u := range upserts {
		f.rows[u.Path] = u
	}
	return removed, nil
}

func (f *fakeVaultStore) Rows(ctx context.Context) (map[string]vault.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]vault.File, len(f.rows))
	for p, r := range f.rows {
		out[p] = r
	}
	return out, nil
}

// demoVault — a synthetic demo vault under t.TempDir (never a real vault).
func demoVault(t *testing.T, notes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range notes {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("demo vault mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("demo vault write %s: %v", name, err)
		}
	}
	return root
}

// indexFixture — wires the fake meta store (cli fakeStore queryFn: meta
// SELECT + the SetMetaKV upsert) and the fake vault scan store, runs one
// `flint index` invocation, and returns the decoded one-line JSON output.
type indexFixture struct {
	*fakeStore
	vault *fakeVaultStore
	meta  map[string]string
	setKV []string // the keys handed to SetMetaKV, in order
}

func newIndexFixture() *indexFixture {
	fx := &indexFixture{
		fakeStore: &fakeStore{},
		vault:     &fakeVaultStore{rows: map[string]vault.File{}},
		meta:      map[string]string{},
	}
	fx.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = $1") {
			if v, ok := fx.meta[args[0].(string)]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil
		}
		if strings.Contains(sql, "INSERT INTO meta") {
			fx.setKV = append(fx.setKV, args[0].(string))
			fx.meta[args[0].(string)] = args[1].(string)
			return nil, nil
		}
		return nil, nil
	}
	return fx
}

// runIndexOn — one `flint index` invocation against a caller-owned fixture
// (the vault snapshot and meta map persist across invocations — the rerun
// semantics under test).
func runIndexOn(t *testing.T, fx *indexFixture, args ...string) (int, map[string]any, string) {
	t.Helper()
	prevStore, prevScan := newStore, indexScanStoreFn
	newStore = func(ctx context.Context, dsn string) (Store, error) { return fx.fakeStore, nil }
	indexScanStoreFn = func(ctx context.Context, dsn string) (vault.Store, func(), error) {
		return fx.vault, func() {}, nil
	}
	t.Cleanup(func() { newStore, indexScanStoreFn = prevStore, prevScan })
	var out strings.Builder
	code := Run(append([]string{"index"}, args...), strings.NewReader(""), &out)
	line := strings.TrimSpace(out.String())
	var m map[string]any
	if line != "" {
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("출력 파싱: %v — %s", err, line)
		}
	}
	return code, m, line
}

// runIndexFake — one invocation on a fresh fixture (single-run cases).
func runIndexFake(t *testing.T, args ...string) (int, map[string]any, string, *indexFixture) {
	t.Helper()
	fx := newIndexFixture()
	code, m, line := runIndexOn(t, fx, args...)
	return code, m, line, fx
}

// TestIndexNewScanCommitsOnce — a first index over a fresh demo vault: exit
// 0, the stats JSON keys in contract order (scanned·changed·new·removed·
// skipped·duration), meta vault_path persisted, vault_last_scan stored, and
// exactly one commit "index: vault files=N changed=M".
func TestIndexNewScanCommitsOnce(t *testing.T) {
	root := demoVault(t, map[string]string{
		"a.md":              "# 데모 노트 A\n첫 문단이다.\n",
		"sub/b.md":          "---\ntags: [demo]\n---\n# 데모 노트 B\n서브 폴더 노트.\n",
		".obsidian/c.md":    "# 스킵 대상\n",
	})
	code, m, _, fx := runIndexFake(t, "--vault", root)
	if code != 0 {
		t.Fatalf("exit=%d out=%v", code, m)
	}
	for _, k := range []string{"scanned", "changed", "new", "removed", "skipped", "duration"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("출력에 %q 누락: %v", k, m)
		}
	}
	if m["scanned"].(float64) != 2 || m["new"].(float64) != 2 {
		t.Fatalf("scanned/new=%v/%v, want 2/2 (.obsidian skipped)", m["scanned"], m["new"])
	}
	if fx.meta["vault_path"] != root {
		t.Fatalf("meta vault_path=%q, want root", fx.meta["vault_path"])
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(fx.meta["vault_last_scan"]), &last); err != nil {
		t.Fatalf("vault_last_scan 파싱: %v — %q", err, fx.meta["vault_last_scan"])
	}
	if last["files"].(float64) != 2 || last["changed"].(float64) != 0 {
		t.Fatalf("vault_last_scan=%v, want files=2 changed=0", last)
	}
	if last["at"] == nil || last["at"].(string) == "" {
		t.Fatalf("vault_last_scan에 at 부재: %v", last)
	}
	if len(fx.commits) != 1 || fx.commits[0] != "index: vault files=2 changed=0" {
		t.Fatalf("commits=%v, want [index: vault files=2 changed=0]", fx.commits)
	}
}

// TestIndexRerunIdempotent — a second run over an unchanged vault: no diff,
// no commit (the one-batch-commit-per-diff contract), vault_last_scan refreshed.
func TestIndexRerunIdempotent(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n본문.\n"})
	fx := newIndexFixture()
	if code, _, _ := runIndexOn(t, fx, "--vault", root); code != 0 {
		t.Fatalf("1차 exit=%d", code)
	}
	code, m, _ := runIndexOn(t, fx)
	if code != 0 {
		t.Fatalf("2차 exit=%d out=%v", code, m)
	}
	if m["new"].(float64) != 0 || m["changed"].(float64) != 0 || m["removed"].(float64) != 0 {
		t.Fatalf("멱등 재실행 stats=%v", m)
	}
	if len(fx.commits) != 1 { // the 1st-run diff commit alone — the rerun added none
		t.Fatalf("디프 없는 재실행 커밋=%v, want 1차 1개만", fx.commits)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(fx.meta["vault_last_scan"]), &last); err != nil {
		t.Fatalf("vault_last_scan 파싱: %v", err)
	}
	if last["files"].(float64) != 1 {
		t.Fatalf("vault_last_scan files=%v, want 1", last["files"])
	}
}

// TestIndexChangedFile — a modified note lands in changed (not new) and the
// commit message carries the changed count.
func TestIndexChangedFile(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n본문.\n"})
	fx := newIndexFixture()
	if code, _, _ := runIndexOn(t, fx, "--vault", root); code != 0 {
		t.Fatalf("1차 exit=%d", code)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# 데모\n수정된 본문.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, m, _ := runIndexOn(t, fx)
	if code != 0 {
		t.Fatalf("2차 exit=%d out=%v", code, m)
	}
	if m["changed"].(float64) != 1 || m["new"].(float64) != 0 {
		t.Fatalf("변경 stats=%v, want changed=1 new=0", m)
	}
	if len(fx.commits) != 2 || fx.commits[1] != "index: vault files=1 changed=1" {
		t.Fatalf("commits=%v, want 2차가 'index: vault files=1 changed=1'", fx.commits)
	}
}

// TestIndexNoVaultPathExit3 — no --vault and no meta vault_path: exit 3 with
// guidance (no path echo — there is none to echo, and none may be invented).
func TestIndexNoVaultPathExit3(t *testing.T) {
	code, m, line, _ := runIndexFake(t)
	if code != 3 {
		t.Fatalf("exit=%d, want 3 — %s", code, line)
	}
	if m["error"] != "usage" {
		t.Fatalf("error 분류=%v, want usage: %s", m["error"], line)
	}
	if !strings.Contains(line, "vault") {
		t.Fatalf("안내에 vault 언급 부재: %s", line)
	}
}

// TestIndexMetaPathReuse — a stored meta vault_path (no flag) is reused as
// the scan root: the vault snapshot carried over from a prior run reports a
// clean no-diff scan over the same notes.
func TestIndexMetaPathReuse(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n본문.\n"})
	fx := newIndexFixture()
	fx.meta[metaVaultPathKey] = root
	code, m, _ := runIndexOn(t, fx)
	if code != 0 {
		t.Fatalf("exit=%d out=%v", code, m)
	}
	if m["scanned"].(float64) != 1 || m["new"].(float64) != 1 {
		t.Fatalf("meta 경로 재사용 stats=%v, want scanned=1 new=1", m)
	}
}

// TestIndexLimitSkippedNote — --limit below the walked count: the excess is
// reported skipped with the "N skipped — rerun" note.
func TestIndexLimitSkippedNote(t *testing.T) {
	root := demoVault(t, map[string]string{
		"a.md": "# A\n본문.\n", "b.md": "# B\n본문.\n", "c.md": "# C\n본문.\n",
	})
	code, m, _, _ := runIndexFake(t, "--vault", root, "--limit", "1")
	if code != 0 {
		t.Fatalf("exit=%d out=%v", code, m)
	}
	if m["skipped"].(float64) != 2 {
		t.Fatalf("skipped=%v, want 2", m["skipped"])
	}
	note, _ := m["note"].(string)
	if !strings.Contains(note, "2 skipped — rerun") {
		t.Fatalf("note=%q, want '2 skipped — rerun' 포함", note)
	}
}

// TestIndexOutputSanitizedNoPathLeak — the vault root path never appears in
// the output JSON, the commit message, or the stored vault_last_scan (the §3
// privacy contract: output goes through the render chokepoint, paths stay
// machine-local).
func TestIndexOutputSanitizedNoPathLeak(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n본문.\n"})
	code, _, line, fx := runIndexFake(t, "--vault", root, "--limit", "0")
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	for _, sink := range append([]string{line}, fx.commits...) {
		if strings.Contains(sink, root) {
			t.Fatalf("경로 유출(%q): %s", root, sink)
		}
	}
	if strings.Contains(fx.meta["vault_last_scan"], root) {
		t.Fatalf("vault_last_scan에 경로 유출: %s", fx.meta["vault_last_scan"])
	}
}

// TestIndexBadArgsExit3 — an unknown flag, a non-numeric --limit, and a
// negative --limit are usage errors (exit 3).
func TestIndexBadArgsExit3(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"--vault", root, "--bogus"},
		{"--vault", root, "--limit", "x"},
		{"--vault", root, "--limit", "-1"},
		{"--vault", ""},
	} {
		code, _, _, _ := runIndexFake(t, args...)
		if code != 3 {
			t.Fatalf("args=%v exit=%d, want 3", args, code)
		}
	}
}

// TestIndexScanErrorPropagates — a scan failure surfaces as the standard
// ErrLine + MapExit (never a silent 0).
func TestIndexScanErrorPropagates(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n"})
	fx := newIndexFixture()
	prevStore, prevScan := newStore, indexScanStoreFn
	newStore = func(ctx context.Context, dsn string) (Store, error) { return fx.fakeStore, nil }
	indexScanStoreFn = func(ctx context.Context, dsn string) (vault.Store, func(), error) {
		return nil, nil, os.ErrNotExist // connect failure shape
	}
	t.Cleanup(func() { newStore, indexScanStoreFn = prevStore, prevScan })
	var out strings.Builder
	code := Run([]string{"index", "--vault", root}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("스캔 실패 exit=0 — %s", out.String())
	}
}

// TestIndexE2ERealSlow — slow (SUPERLITE_REAL=1 + FLINT_TEST_DSN): the full
// real path on an isolated scratch DB — real newRealStore, real vault.Open
// pool, real vault.Scan, real dolt commits. A synthetic demo vault: first
// index (new), a flag-less rerun via the persisted meta vault_path
// (idempotent), a modified note (changed), and the status vault row.
func TestIndexE2ERealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(base.Close)

	name := fmt.Sprintf("flint_v1210_t3_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
		var last error
		for i := 0; i < 3; i++ {
			if _, last = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name); last == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("스크래치 DB 잔존(%q): %v", name, last)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	freshDSN := u.String()

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })
	// the scan-store seam stays REAL (vault.Open) — pointed at the scratch DB
	// via the resolved DSN (FLINT_DSN takes precedence).
	t.Setenv("FLINT_DSN", freshDSN)

	root := demoVault(t, map[string]string{
		"alpha.md":        "# 데모 알파\n첫 번째 데모 노트 본문.\n",
		"notes/beta.md":   "---\ntags: [demo, vault]\n---\n# 데모 베타\n두 번째 데모 노트.\n",
		".obsidian/x.md":  "# 스킵\n",
	})

	run := func(args ...string) map[string]any {
		t.Helper()
		var out strings.Builder
		code := Run(append([]string{"index"}, args...), strings.NewReader(""), &out)
		if code != 0 {
			t.Fatalf("index %v exit=%d — %s", args, code, out.String())
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
			t.Fatalf("출력 파싱: %v — %s", err, out.String())
		}
		return m
	}
	statusLine := func() string {
		t.Helper()
		var out strings.Builder
		if code := Run([]string{"status"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("status exit=%d — %s", code, out.String())
		}
		return strings.TrimSpace(out.String())
	}

	// 1. first index — 2 .md files (.obsidian skipped).
	m := run("--vault", root)
	if m["scanned"].(float64) != 2 || m["new"].(float64) != 2 {
		t.Fatalf("1차 stats=%v, want scanned=2 new=2", m)
	}

	// 2. flag-less rerun via the persisted meta vault_path — idempotent.
	m = run()
	if m["new"].(float64) != 0 || m["changed"].(float64) != 0 || m["removed"].(float64) != 0 {
		t.Fatalf("재실행 stats=%v, want 디프 없음", m)
	}

	// 3. status carries the vault row in key order (after the previous tail).
	line := statusLine()
	for _, k := range []string{`"hot_bytes_p95":`, `"vault_indexed_at":`, `"vault_files":2`, `"vault_changed":0`} {
		if !strings.Contains(line, k) {
			t.Fatalf("status에 %q 부재: %s", k, line)
		}
	}
	if strings.Index(line, `"vault_indexed_at":`) < strings.Index(line, `"hot_bytes_p95":`) {
		t.Fatalf("vault 행이 기존 키 앞에 삽입됨: %s", line)
	}

	// 4. a modified note — changed=1, status vault_changed follows.
	if err := os.WriteFile(filepath.Join(root, "alpha.md"),
		[]byte("# 데모 알파\n수정된 데모 노트 본문.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = run()
	if m["changed"].(float64) != 1 || m["new"].(float64) != 0 {
		t.Fatalf("변경 stats=%v, want changed=1", m)
	}
	line = statusLine()
	if !strings.Contains(line, `"vault_changed":1`) {
		t.Fatalf("status vault_changed 갱신 안 됨: %s", line)
	}
	if strings.Contains(line, root) {
		t.Fatalf("status에 볼트 경로 유출: %s", line)
	}
}
