// tuning_meta_v0342_test.go — v0.34.2 R1: tuning 키 단일 소스 계약.
// tuningCurrent·tuningResetAll이 하드코딩 6키 목록을 복제해
// backup_encrypt(7번째 meta 키)가 누락되던 드리프트 방어.
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

// resetAllCaptureFake — meta 삭제 위임(db.Repo.MetaDelete)의 인자(삭제
// 대상 키)를 기록. v0.41.0 T3: DELETE 리터럴은 db 계층 소유(쓰기 가드).
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

// TestTuningResetAllCoversAllKeys — reset --all이 tuningKeyAll() 전체를
// 삭제 대상으로 순회(하드코딩 리터럴 없음)하는지 동작으로 입증.
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

// TestTuningMetaValueSanitized — v1.0.1 감사(F4): meta 원시 값(vocab_*는
// 자유 문자열)이 get·list의 value 출력에서 살균 출구를 통과한다 —
// 미살균이면 [flint] 마커가 원문 그대로 노출된다.
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

// listMetaFake — tuningCurrent의 meta IN 조회(키는 SQL 리터럴로 조립,
// args=nil)에서 SQL에 키 리터럴이 있는 meta 행만 반환.
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

// TestTuningListReportsBackupEncryptOverride — 증상 회귀 방어:
// backup_encrypt 오버라이드가 list에서 override:true로 보고되어야
// 한다(tuningCurrent 조회 키 집합이 tuningKeyAll과 동일해야 함).
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
