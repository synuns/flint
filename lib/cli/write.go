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

// ErrInvalidSource — --source 무효값(exit 3). 유입 계측 소스는 3종뿐이다.
var ErrInvalidSource = errors.New("write: invalid --source (hook|manual|mcp)")

// validSources — 계측 소스 허용값(spec §1: 훅 hook · 사람/기본 manual · MCP mcp).
var validSources = map[string]bool{"hook": true, "manual": true, "mcp": true}

// ParseSourceFlag — args에서 --source 값을 파싱한다(플랜 T61). 미지정은
// "manual", 무효값·값 부재는 ErrInvalidSource(CLI는 exit 3).
func ParseSourceFlag(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != "--source" {
			continue
		}
		if i+1 >= len(args) {
			return "", ErrInvalidSource
		}
		src := args[i+1]
		if !validSources[src] {
			return "", ErrInvalidSource
		}
		return src, nil
	}
	return "manual", nil
}

// UsageStore — 유입 계측(RecordUsage, T60)이 가능한 저장소(maintain의
// MaintStore와 같은 단언 패턴). 미구현 Store는 recordUsage가 no-op로 건너뛴다.
type UsageStore interface {
	RecordUsage(ctx context.Context, kind, source string) error
}

// recordUsage — Store가 UsageStore면 유입 1회를 기록한다. 미구현 no-op,
// 실패는 조용히(계측이 쓰기 결과·exit 코드를 깨지 않는다 — 플랜 T61).
func recordUsage(ctx context.Context, st Store, kind, source string) {
	if us, ok := st.(UsageStore); ok {
		_ = us.RecordUsage(ctx, kind, source)
	}
}

// runWrite — stdin JSON/JSONL 다중을 처리한다. 커밋은 배치 1회, dedup은
// 줄별(duplicate 줄 skip·신규 줄 부분 커밋; 전부 duplicate면 0건 커밋).
func runWrite(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	kind, session := "", ""
	var pos []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
			continue
		}
		if args[i] == "--source" && i+1 < len(args) {
			i++ // 값은 ParseSourceFlag에서 검증(아래)
			continue
		}
		pos = append(pos, args[i])
	}
	if len(pos) > 0 {
		kind = pos[0]
	}
	source, err := ParseSourceFlag(args)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 3
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

	// 검증 → 살균 → 정규화 → 해시 dedup(줄별 skip) → 판정. 검증 실패에서만 즉시
	// 반환(exit 3). duplicate 줄은 배치 전체를 폐기하지 않고 skip — 부분 커밋.
	recs := make([]map[string]any, 0, len(payloads))
	dropped := map[string]bool{}
	var warnings []string
	hash, state := "", "active"
	dups := 0
	seen := map[string]bool{} // 배치 내 동일 content_hash 줄: InsertBatch 전체 실패 방지
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
			// 어휘 검증(T35): Normalize된 domain과 tags 기준 경고 병합 —
			// 경고만(저장 차단 아님·exit 불변). 살균 경고 뒤 순서 유지.
			if raw, ok := p["tags"].([]any); ok {
				tags := make([]string, 0, len(raw))
				for _, tag := range raw {
					if s, ok := tag.(string); ok {
						tags = append(tags, s)
					}
				}
				domain, _ := n["domain"].(string)
				warnings = append(warnings, store.CheckVocabulary(tags, domain)...)
			}
			// 해시 입력은 휘발성 필드(valid_at 등) 제외 경로(HashCanonical)여야
			// 동일 payload 재전송이 duplicate로 판정된다(시딩 멱등).
			h := store.ContentHash(store.HashCanonical(n))
			if seen[h] { // 같은 배치 내 중복 줄도 skip 대상
				dups++
				continue
			}
			seen[h] = true
			exists, err := st.ExistsHash(ctx, h)
			if err != nil {
				fmt.Fprintln(stdout, errLine(err))
				return store.MapExit(err)
			}
			if exists {
				dups++ // 줄별 dedup: 이 줄만 skip, 나머지 신규 줄은 커밋된다
				continue
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

	if len(recs) == 0 {
		// 전부 duplicate: 시딩 멱등 — 0건 커밋·exit 0. 유입 계측(T61)은 write
		// 1회 + dup(skip 줄 수)회, 이중 계수 금지.
		recordUsage(ctx, st, "write", source)
		recordDups(ctx, st, dups)
		fmt.Fprintln(stdout, store.OutputJSON("duplicate", "", "", 0))
		return 0
	}
	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			// 레이스(ExistsHash 통과 후 동시 삽입): 트랜잭션 전체 롤백 상태.
			recordUsage(ctx, st, "write", source)
			recordUsage(ctx, st, "dup", "")
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
	commitHash, err := st.Commit(ctx, msg)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	var out string
	if len(payloads) == 1 {
		// 단일 줄은 기존 {status,hash,state,id} 불변(하위호환).
		id := 0
		if len(ids) > 0 {
			id = ids[0]
		}
		out = store.OutputJSON("committed", hash, state, id)
	} else {
		// 다중 줄: 마지막 줄 hash·state + 첫 줄 id 혼합 대신 집계형 단일 커밋 해시.
		out = multiCommitOutput(len(recs), dups, commitHash)
	}
	if len(dropped) > 0 || len(warnings) > 0 {
		out = withWarnings(out, dropped, warnings)
	}
	recordUsage(ctx, st, "write", source) // 신규 커밋 성공 후 유입 1회(T61)
	recordDups(ctx, st, dups)             // 혼합 배치의 skip 줄도 dup 계측
	fmt.Fprintln(stdout, out)
	return 0
}

// recordDups — skip된 duplicate 줄 수만큼 dup 유입을 계측한다(0은 no-op).
func recordDups(ctx context.Context, st Store, n int) {
	for i := 0; i < n; i++ {
		recordUsage(ctx, st, "dup", "")
	}
}

// multiCommitOutput — JSONL 다중(2줄 이상) 커밋 출력. 줄별 hash·state·id
// 혼합 대신 집계(inserted·duplicates)와 배치 커밋 해시 1개만 남긴다.
func multiCommitOutput(inserted, duplicates int, commitHash string) string {
	b, err := json.Marshal(map[string]any{
		"status":     "committed",
		"inserted":   inserted,
		"duplicates": duplicates,
		"hash":       commitHash,
	})
	if err != nil {
		return `{"status":"committed"}`
	}
	return string(b)
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
