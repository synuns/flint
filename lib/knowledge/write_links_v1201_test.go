// write_links_v1201_test.go — v1.20.1 patch regressions (r84–r87) on
// the write-path relations handling: the mixed-batch duplicate-link
// accrual (r84), the int64 overflow·error-propagation of the link
// target precheck (r86), the self-reference refusal (r86), and the
// relation length cap (r87). Fake-store level; the real-DB idempotence
// (unique index·ON CONFLICT) is covered by the lib/db slow line.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// mixedBatchQueryFn — the fake query profile of a mixed 2-line batch:
// line 1 is a duplicate of existing row 9 (content_hash ANY lookup),
// every requested link target id exists (id ANY lookup — the arg is
// []int64 since v1.20.1).
func mixedBatchQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "content_hash = ANY($1)") {
		hashes, _ := args[0].([]string)
		return [][]any{{hashes[0], int32(9), "mixed-dup-topic", "active"}}, nil
	}
	if strings.Contains(sql, "id = ANY($1") && strings.Contains(sql, "FROM knowledge") {
		ids, _ := args[0].([]int64)
		var out [][]any
		for _, id := range ids {
			out = append(out, []any{int32(id)})
		}
		return out, nil
	}
	return nil, nil
}

// TestWriteMixedBatchAccruesDuplicateLinksV1201 — r84: a batch holding
// BOTH a duplicate resend (links to={2,duplicates}) and a new line
// (links to={5,supports}) must accrue BOTH links. The pre-patch code
// flushed pendLinks only on the all-duplicate paths — the mixed batch
// dropped the duplicate-path link silently.
func TestWriteMixedBatchAccruesDuplicateLinksV1201(t *testing.T) {
	st := &fakeStore{queryFn: mixedBatchQueryFn}
	var out bytes.Buffer
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 mixed dup","summary":"dup line","confidence":0.9,"relations":[{"to":2,"relation":"duplicates"}]}`+"\n"+
			`{"topic":"v1201 mixed new","summary":"new line","confidence":0.9,"relations":[{"to":5,"relation":"supports"}]}`+"\n"),
		&out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out.String())
	}
	want := [][3]any{{1, 5, "supports"}, {9, 2, "duplicates"}} // fresh row id 1 first (the fake InsertBatch contract), then the dup resend on row 9
	if len(st.linkInserts) != len(want) {
		t.Fatalf("linkInserts=%v want %v (mixed batch must not drop the duplicate-path link)", st.linkInserts, want)
	}
	for i, w := range want {
		if st.linkInserts[i] != w {
			t.Fatalf("linkInserts[%d]=%v want %v", i, st.linkInserts[i], w)
		}
	}
	// Both link families ride the ONE batch commit — no standalone
	// "relations accrual (duplicate resend)" commit on the mixed path.
	for _, c := range st.commits {
		if strings.Contains(c, "relations accrual (duplicate resend)") {
			t.Fatalf("mixed path made a standalone dup-link commit: %v", st.commits)
		}
	}
	if len(st.commits) == 0 {
		t.Fatal("no commit at all — links·rows must ride one commit")
	}
}

// TestWriteLinkTargetInt64OverflowRefusedV1201 — r86: a to at or beyond
// 2^63 is refused at parse time (exit 3) — it can never be a knowledge
// id (bigserial = int64) and previously flowed into the batched ANY($1)
// lookup where the array encoding failed opaquely.
func TestWriteLinkTargetInt64OverflowRefusedV1201(t *testing.T) {
	st := &fakeStore{}
	var out bytes.Buffer
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 overflow","summary":"s","confidence":0.9,"relations":[{"to":9223372036854775808,"relation":"refines"}]}`),
		&out)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (2^63 refusal): %s", code, out.String())
	}
	if len(st.linkInserts) != 0 || len(st.inserted) != 0 {
		t.Fatalf("side effects on refusal: links=%v rows=%v", st.linkInserts, st.inserted)
	}
}

// TestWritePrecheckLookupErrorPropagatesV1201 — r86: a failure of the
// id-lookup pre-query is propagated (exit non-zero), not silently
// treated as "every target exists".
func TestWritePrecheckLookupErrorPropagatesV1201(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id = ANY($1") && strings.Contains(sql, "FROM knowledge") {
			return nil, store.ErrServerDown
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 lookup err","summary":"s","confidence":0.9,"relations":[{"to":4,"relation":"refines"}]}`),
		&out)
	if code == 0 {
		t.Fatalf("exit=0 — lookup failure must propagate, out=%s", out.String())
	}
	if len(st.linkInserts) != 0 || len(st.inserted) != 0 {
		t.Fatalf("side effects after lookup failure: links=%v rows=%v", st.linkInserts, st.inserted)
	}
}

// TestWriteSelfReferenceRefusedV1201 — r86: a duplicate resend whose
// relation targets the existing row itself (to == DupMeta.ID) is
// refused up front (exit 3).
func TestWriteSelfReferenceRefusedV1201(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "content_hash = ANY($1)") {
			hashes, _ := args[0].([]string)
			return [][]any{{hashes[0], int32(9), "self-ref", "active"}}, nil
		}
		return nil, nil
	}}
	var out bytes.Buffer
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 selfref","summary":"s","confidence":0.9,"relations":[{"to":9,"relation":"refines"}]}`),
		&out)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (duplicate-path self-reference): %s", code, out.String())
	}
	if len(st.linkInserts) != 0 {
		t.Fatalf("self-referencing link accrued: %v", st.linkInserts)
	}
}

// TestInsertKnowledgeLinksSelfReferenceGuardV1201 — r86: the accrual
// helper itself refuses to == from (the fresh-row defense — the from id
// only exists after InsertBatch there).
func TestInsertKnowledgeLinksSelfReferenceGuardV1201(t *testing.T) {
	st := &fakeStore{}
	if err := insertKnowledgeLinks(context.Background(), st, 7,
		[]linkSpec{{To: 8, Relation: "refines"}, {To: 7, Relation: "duplicates"}}); err == nil {
		t.Fatal("self-referencing spec accepted")
	}
	if len(st.linkInserts) != 0 {
		t.Fatalf("specs after the self-reference must not accrue: %v", st.linkInserts)
	}
}

// TestWriteRelationLengthCapV1201 — r87: a relation value over 64 bytes
// is refused at parse time (exit 3); the longest registered vocabulary
// value is 12 bytes.
func TestWriteRelationLengthCapV1201(t *testing.T) {
	long := strings.Repeat("r", 65)
	st := &fakeStore{}
	var out bytes.Buffer
	code := RunWrite(context.Background(), st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 long rel","summary":"s","confidence":0.9,"relations":[{"to":2,"relation":"`+long+`"}]}`),
		&out)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (relation length cap): %s", code, out.String())
	}
	if len(st.linkInserts) != 0 || len(st.inserted) != 0 {
		t.Fatalf("side effects on length refusal: links=%v rows=%v", st.linkInserts, st.inserted)
	}
}
