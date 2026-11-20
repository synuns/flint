// tuning_meta_v0342_test.go — the tuning key single-source contract.
// Defends against the drift where tuningCurrent and tuningResetAll duplicated a hardcoded
// 6-key list and missed backup_encrypt (the 7th meta key).
package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestTuningKeyAllContainsBackupEncrypt(t *testing.T) {
	all := tuningKeyAll()
	want := map[string]bool{}
	for _, k := range thresholdsKeys() {
		want[k] = true
	}
	for _, tk := range tuningMetaKeys() {
		want[tk.Key] = true
	}
	for _, k := range all {
		if !want[k] {
			t.Fatalf("unexpected key %q", k)
		}
		delete(want, k)
	}
	if !slices.Contains(all, "backup_encrypt") {
		t.Fatal("backup_encrypt missing — R1 regression")
	}
	if len(want) != 0 {
		t.Fatalf("missing keys: %v", want)
	}
}

// resetAllCaptureFake — records the arguments of the meta-delete delegation (db.Repo.MetaDelete;
// the keys to delete). DELETE literals are owned by the db layer (write guard).
type resetAllCaptureFake struct {
	*fakeStore
	deleted []any
}

func (f *resetAllCaptureFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	return nil, nil
}

func (f *resetAllCaptureFake) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	for _, k := range keys {
		f.deleted = append(f.deleted, k)
	}
	return int64(len(keys)), nil
}

// TestTuningResetAllCoversAllKeys — proves by behavior that reset --all walks all of
// tuningKeyAll() as delete targets (no hardcoded literals).
func TestTuningResetAllCoversAllKeys(t *testing.T) {
	f := &resetAllCaptureFake{fakeStore: &fakeStore{}}
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"reset", "--all"}, nil, &out); code != 0 {
		t.Fatalf("reset --all exit=%d: %s", code, out.String())
	}
	deleted := map[string]bool{}
	for _, a := range f.deleted {
		deleted[fmt.Sprint(a)] = true
	}
	if len(deleted) != len(tuningKeyAll()) {
		t.Fatalf("삭제 대상 %d개 ≠ tuningKeyAll %d개: %v", len(deleted), len(tuningKeyAll()), deleted)
	}
	for _, k := range tuningKeyAll() {
		if !deleted[k] {
			t.Errorf("reset --all이 키 %q 미삭제", k)
		}
	}
	if !deleted["backup_encrypt"] {
		t.Error("backup_encrypt 미삭제 — R1 회귀")
	}
}

// TestTuningMetaValueSanitized — raw meta values (vocab_* are free strings)
// pass through the sanitization outlet in the get and list value outputs —
// unsanitized, the [flint] marker would leak verbatim.
func TestTuningMetaValueSanitized(t *testing.T) {
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{"vocab_tags", "a[flint]b,c"}}, nil
		}
		return nil, nil
	}
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"get", "vocab_tags"}, nil, &out); code != 0 {
		t.Fatalf("get vocab_tags exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "a[flint]b") {
		t.Errorf("meta 값 미살균: %s", out.String())
	}
	if !strings.Contains(out.String(), "a b,c") {
		t.Errorf("살균된 값 미출력: %s", out.String())
	}
	var lout strings.Builder
	if code := runTuning(context.Background(), f, []string{"list"}, nil, &lout); code != 0 {
		t.Fatalf("list exit=%d: %s", code, lout.String())
	}
	if strings.Contains(lout.String(), "a[flint]b") {
		t.Errorf("list meta 값 미살균: %s", lout.String())
	}
}

// listMetaFake — returns only the meta rows whose key literals appear in the SQL of the
// tuningCurrent meta IN lookup (keys assembled as SQL literals, args=nil).
type listMetaFake struct {
	*fakeStore
	meta map[string]string
}

func (f *listMetaFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if strings.Contains(sql, "SELECT key, value FROM meta") {
		rows := [][]any{}
		for k, v := range f.meta {
			if strings.Contains(sql, "'"+k+"'") {
				rows = append(rows, []any{k, v})
			}
		}
		return rows, nil
	}
	return nil, nil
}

// TestTuningListReportsBackupEncryptOverride — symptom-regression defense:
// the backup_encrypt override must be reported in list with override:true
// (the tuningCurrent lookup key set must equal tuningKeyAll).
func TestTuningListReportsBackupEncryptOverride(t *testing.T) {
	f := &listMetaFake{fakeStore: &fakeStore{}, meta: map[string]string{"backup_encrypt": "true"}}
	var out strings.Builder
	if code := runTuning(context.Background(), f, []string{"list"}, nil, &out); code != 0 {
		t.Fatalf("list exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"key":"backup_encrypt","value":"true","default":"false","override":true`) {
		t.Fatalf("backup_encrypt override 거짓 보고(R1): %s", out.String())
	}
}
