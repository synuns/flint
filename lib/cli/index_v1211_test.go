// index_v1211_test.go — v1.21.1 (r91-r94) `flint index` regressions: the
// vault-root guard (a file/typo path is exit 3 BEFORE any scan), the
// persist-after-success ordering (a failed scan leaves meta vault_path
// unset), threshold overrides wiring, the snapshot-replacement notice, and
// the touch-only pointer commit. Synthetic demo vaults only (§3).
package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/vault"
)

// TestIndexVaultRootGuardExit3 — an explicit --vault pointing at a FILE
// (or a nonexistent path) is a usage exit 3 before the scan, and nothing
// is persisted (the typo-path persistence the finding banned).
func TestIndexVaultRootGuardExit3(t *testing.T) {
	tmp := t.TempDir()
	notAVault := filepath.Join(tmp, "not-a-vault.md")
	if err := os.WriteFile(notAVault, []byte("# 파일\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--vault", notAVault},
		{"--vault", filepath.Join(tmp, "no-such-dir")},
	} {
		code, m, line, fx := runIndexFake(t, args...)
		if code != 3 {
			t.Fatalf("args=%v exit=%d, want 3 — %s", args, code, line)
		}
		if m["error"] != "usage" {
			t.Fatalf("args=%v error=%v, want usage: %s", args, m["error"], line)
		}
		if strings.Contains(line, tmp) {
			t.Fatalf("가드 오류에 경로 유출: %s", line)
		}
		if _, ok := fx.meta[metaVaultPathKey]; ok {
			t.Fatalf("args=%v: 실패 경로가 meta에 영속됨", args)
		}
	}
}

// TestIndexScanErrorPersistsNothing — a scan failure AFTER the guard
// (connect/scan error) still leaves meta vault_path unset: the persist
// moved after a successful scan (the r91-2/r93-6/r94-2 shape).
func TestIndexScanErrorPersistsNothing(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n"})
	fx := newIndexFixture()
	prevStore, prevScan := newStore, indexScanStoreFn
	newStore = func(ctx context.Context, dsn string) (Store, error) { return fx.fakeStore, nil }
	indexScanStoreFn = func(ctx context.Context, dsn string) (vault.Store, func(), error) {
		return nil, nil, os.ErrNotExist
	}
	t.Cleanup(func() { newStore, indexScanStoreFn = prevStore, prevScan })
	var out strings.Builder
	code := Run([]string{"index", "--vault", root}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("스캔 실패 exit=0 — %s", out.String())
	}
	if _, ok := fx.meta[metaVaultPathKey]; ok {
		t.Fatalf("실패한 스캔이 vault_path를 영속화: %v", fx.meta)
	}
	if len(fx.commits) != 0 {
		t.Fatalf("실패 실행의 커밋=%v", fx.commits)
	}
}

// TestIndexThresholdOverridesWired — the 3 vault threshold keys reach
// vault.Options through LoadThresholds (meta overrides; unset falls back
// to the code defaults — the TUNING §2 contract restored).
func TestIndexThresholdOverridesWired(t *testing.T) {
	notes := map[string]string{
		"a.md":      "# A\n본문.\n",
		"b.md":      "# B\n본문.\n",
		"c.md":      "# C\n본문.\n",
		"hide/d.md": "# D\n본문.\n",
	}
	root := demoVault(t, notes)
	fx := newIndexFixture()
	base := fx.queryFn
	fx.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "vault_scan_max_files") {
			return [][]any{{"vault_scan_max_files", "2"}}, nil
		}
		return base(sql, args)
	}
	code, m, _ := runIndexOn(t, fx, "--vault", root)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if m["scanned"].(float64) != 2 || m["skipped"].(float64) != 2 {
		t.Fatalf("vault_scan_max_files=2 미적용: %v", m)
	}

	// vault_skip_dirs override: "hide" replaces the default list — the
	// default .obsidian skip must give way to the declared list.
	root2 := demoVault(t, map[string]string{
		"a.md":           "# A\n본문.\n",
		"hide/h.md":      "# H\n본문.\n",
		".obsidian/o.md": "# O\n본문.\n",
	})
	fx2 := newIndexFixture()
	base2 := fx2.queryFn
	fx2.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "vault_scan_max_files") {
			return [][]any{{"vault_skip_dirs", "hide"}}, nil
		}
		return base2(sql, args)
	}
	code, m, _ = runIndexOn(t, fx2, "--vault", root2)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if m["scanned"].(float64) != 2 {
		t.Fatalf("vault_skip_dirs=hide 미적용: %v (want scanned=2: a.md + .obsidian/o.md)", m)
	}
	if len(fx2.vault.rows) != 2 {
		t.Fatalf("rows=%v, want 2", fx2.vault.rows)
	}
	if _, ok := fx2.vault.rows["hide/h.md"]; ok {
		t.Fatalf("hide/ 스킵 실패: %v", fx2.vault.rows)
	}
}

// TestIndexVaultPathChangeNotice — an explicit --vault differing from the
// stored meta path emits the snapshot-replacement notice (guidance only,
// path-free) and re-points meta after the successful scan; a same-path
// rerun emits nothing.
func TestIndexVaultPathChangeNotice(t *testing.T) {
	rootA := demoVault(t, map[string]string{"a.md": "# A\n본문.\n"})
	rootB := demoVault(t, map[string]string{"b.md": "# B\n본문.\n"})
	fx := newIndexFixture()
	if code, _, _ := runIndexOn(t, fx, "--vault", rootA); code != 0 {
		t.Fatalf("1차 exit=%d", code)
	}
	code, m, line := runIndexOn(t, fx, "--vault", rootB)
	if code != 0 {
		t.Fatalf("2차 exit=%d", code)
	}
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "vault path changed") || !strings.Contains(notice, "replaced") {
		t.Fatalf("교체 예고 부재: %q — %s", notice, line)
	}
	if strings.Contains(notice, rootA) || strings.Contains(notice, rootB) {
		t.Fatalf("예고에 경로 유출: %q", notice)
	}
	if m["removed"].(float64) != 1 {
		t.Fatalf("removed=%v, want 1 (a.md snapshot replaced)", m["removed"])
	}
	if fx.meta[metaVaultPathKey] != rootB {
		t.Fatalf("meta vault_path=%q, want rootB", fx.meta[metaVaultPathKey])
	}
	// same-path explicit rerun: no notice.
	code, m, _ = runIndexOn(t, fx, "--vault", rootB)
	if code != 0 {
		t.Fatalf("3차 exit=%d", code)
	}
	if _, ok := m["notice"]; ok {
		t.Fatalf("동일 경로 재실행에 예고 발생: %v", m["notice"])
	}
}

// TestIndexTouchOnlyCommitsPointer — a pure mtime touch (same content)
// commits the pointer refresh ("changed=0" — the touch is excluded from
// changed) and the stats JSON stays all-zero deltas.
func TestIndexTouchOnlyCommitsPointer(t *testing.T) {
	root := demoVault(t, map[string]string{"a.md": "# 데모\n본문.\n"})
	fx := newIndexFixture()
	if code, _, _ := runIndexOn(t, fx, "--vault", root); code != 0 {
		t.Fatalf("1차 exit=%d", code)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "a.md"), future, future); err != nil {
		t.Fatal(err)
	}
	code, m, _ := runIndexOn(t, fx)
	if code != 0 {
		t.Fatalf("2차 exit=%d", code)
	}
	for _, k := range []string{"new", "changed", "removed", "skipped"} {
		if m[k].(float64) != 0 {
			t.Fatalf("touch-only stats[%s]=%v, want 0", k, m[k])
		}
	}
	if len(fx.commits) != 2 || fx.commits[1] != "index: vault files=1 changed=0" {
		t.Fatalf("commits=%v, want 포인터 갱신 커밋 추가", fx.commits)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(fx.meta["vault_last_scan"]), &last); err != nil {
		t.Fatal(err)
	}
}
