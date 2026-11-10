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

// resetAllCaptureFake — DELETE FROM meta의 인자(삭제 대상 키)를 기록.
type resetAllCaptureFake struct {
	*fakeStore
	deleted []any
}

func (f *resetAllCaptureFake) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	if strings.Contains(sql, "DELETE FROM meta") {
		f.deleted = append(f.deleted, args...)
	}
	return nil, nil
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
