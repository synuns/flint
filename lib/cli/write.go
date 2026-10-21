// write.go — flint write <kind>: 5단계 파이프라인(검증→dedup→판정→커밋→출력).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"flint/lib/store"
)

// kindColumns — kind별 저장 가능 컬럼(spec §6). payload의 비컬럼 키(예: session의
// tasks, project의 note)는 메타데이터라 버린다.
var kindColumns = map[string]map[string]bool{
	"knowledge": {"topic": true, "summary": true, "tags": true, "entity": true,
		"domain": true, "confidence": true, "origin": true, "status": true,
		"content_hash": true, "session_id": true, "valid_at": true},
	"session": {"ts": true, "agent": true, "summary": true},
	"project": {"name": true, "status": true, "updated": true},
}

// runWrite — stdin JSON/JSONL 다중(단일 커밋, all-or-nothing)을 처리한다.
func runWrite(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	kind, session := "", ""
	var pos []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
			continue
		}
		pos = append(pos, args[i])
	}
	if len(pos) > 0 {
		kind = pos[0]
	}

	payloads, err := readPayloads(stdin)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 3
	}
	if len(payloads) == 0 {
		fmt.Fprintln(stdout, errLine(&store.SchemaError{Field: "payload", Expect: "non-empty JSON/JSONL on stdin"}))
		return 3
	}

	// 검증 → 살균 → 정규화 → 해시 dedup → 판정. 첫 실패에서 즉시 반환(exit 3/0).
	recs := make([]map[string]any, 0, len(payloads))
	dropped := map[string]bool{}
	var warnings []string
	hash, state := "", "active"
	for _, p := range payloads {
		if se := store.Validate(kind, p); se != nil {
			fmt.Fprintln(stdout, errLine(se))
			return 3
		}
		// 살균(ADR-0006): 저장 전 제어 문자·[flint] 마커 중립화 + 비밀 스캔.
		// 해시 계산 전 단계라 살균 결과가 곧 저장·해시 본문이 된다.
		warnings = append(warnings, sanitizePayload(p)...)
		n := store.Normalize(kind, p)
		if kind == "knowledge" {
			// 해시 입력은 휘발성 필드(valid_at 등) 제외 경로(HashCanonical)여야
			// 동일 payload 재전송이 duplicate로 판정된다(시딩 멱등).
			h := store.ContentHash(store.HashCanonical(n))
			exists, err := st.ExistsHash(ctx, h)
			if err != nil {
				fmt.Fprintln(stdout, errLine(err))
				return store.MapExit(err)
			}
			if exists {
				fmt.Fprintln(stdout, store.OutputJSON("duplicate", "", "", 0))
				return 0
			}
			conf, _ := n["confidence"].(float64)
			state = store.Classify(kind, conf)
			n["content_hash"] = h
			n["status"] = state
			hash = h
		}
		n["kind"] = kind
		for k := range n {
			if !kindColumns[kind][k] && k != "kind" {
				delete(n, k)
				dropped[k] = true
			}
		}
		recs = append(recs, n)
	}

	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			fmt.Fprintln(stdout, store.OutputJSON("duplicate", "", "", 0))
			return 0
		}
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	msg := fmt.Sprintf("%s: %v", kind, recs[0]["summary"])
	if session != "" {
		msg += fmt.Sprintf(" [session:%s]", session)
	}
	if _, err := st.Commit(ctx, msg); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	id := 0
	if len(ids) > 0 {
		id = ids[0]
	}
	out := store.OutputJSON("committed", hash, state, id)
	if len(dropped) > 0 || len(warnings) > 0 {
		out = withWarnings(out, dropped, warnings)
	}
	fmt.Fprintln(stdout, out)
	return 0
}

// sanitizeFields — 살균 대상 텍스트 컬럼(kind 공통 최소집합).
var sanitizeFields = []string{"topic", "summary", "entity", "agent", "name"}

// sanitizePayload — 텍스트 필드를 살균해 원본 맵을 갱신하고, 비밀 후보 경고
// ("possible secret: 패턴명")를 반환한다. 저장 거부 아님(경고+살균).
func sanitizePayload(p map[string]any) []string {
	var warns []string
	for _, f := range sanitizeFields {
		s, ok := p[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			p[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	return warns
}

// withWarnings — 출력 한 줄 JSON에 경고 배열(dropped keys·비밀 스캔 병합)을
// 추가한다(무경고 삭제·감지 누락 방지).
func withWarnings(line string, dropped map[string]bool, secretWarns []string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	w := append([]string{}, secretWarns...)
	if len(dropped) > 0 {
		keys := make([]string, 0, len(dropped))
		for k := range dropped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w = append(w, "dropped keys: "+strings.Join(keys, ","))
	}
	m["warnings"] = w
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

// readPayloads — stdin을 줄 단위 JSON(또는 한 덩어리 JSON)으로 파싱한다.
func readPayloads(stdin io.Reader) ([]map[string]any, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stdin); err != nil {
		return nil, &store.SchemaError{Field: "stdin", Expect: "readable"}
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	var out []map[string]any
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, &store.SchemaError{Field: "payload", Expect: "valid JSON object per line"}
		}
		out = append(out, p)
	}
	return out, nil
}
