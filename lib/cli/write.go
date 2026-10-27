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

// UsageCountStore — count회를 일괄 증분하는 유입 계측 확장. recordDups가
// dup N을 1회 호출로 기록한다(O(N) 읽기-쓰기 회피 — db.Repo.RecordUsageCount).
// 미구현 Store(레거시 fake)는 recordDups가 1회씩 N번 폴백.
type UsageCountStore interface {
	RecordUsageCount(ctx context.Context, kind, source string, count int) error
}

// recordUsage — Store가 UsageStore면 유입 1회를 기록한다. 미구현 no-op,
// 실패는 조용히(계측이 쓰기 결과·exit 코드를 깨지 않는다 — 플랜 T61).
func recordUsage(ctx context.Context, st Store, kind, source string) {
	if us, ok := st.(UsageStore); ok {
		_ = us.RecordUsage(ctx, kind, source)
	}
}

// writeThresholds — loadThresholds(maintain.go 소유) 재사용(doneThresholds
// 관례 승계): 판정 임계값은 쓰기 시점 meta 값. 읽기 실패 시 기본값 폴백
// (비간섭: 판정 로드가 쓰기 결과·exit를 바꾸지 않는다).
func writeThresholds(ctx context.Context, st Store) store.Thresholds {
	th, err := loadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
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
	// 판정 floor는 이번 쓰기가 읽은 meta conf_floor(값 전달 — 전역 주입 없음).
	floor := writeThresholds(ctx, st).ConfFloor
	recs := make([]map[string]any, 0, len(payloads))
	dropped := map[string]bool{}
	var warnings []string
	hash, state := "", "active"
	dups := 0
	// 배치 내 동일 키 줄(knowledge content_hash·project name): InsertBatch 전체 실패 방지
	seen := map[string]bool{}
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
			state = store.Classify(kind, conf, floor)
			n["content_hash"] = h
			n["status"] = state
			hash = h
		}
		if kind == "project" {
			// 자연키 name 줄별 dedup(knowledge content_hash와 동일 계약 —
			// unique(name) 충돌을 배치 전에 skip해 시딩 멱등 확보).
			name, _ := n["name"].(string)
			if seen["project:"+name] { // 같은 배치 내 동일 name 줄도 skip 대상
				dups++
				continue
			}
			seen["project:"+name] = true
			exists, err := st.ExistsProject(ctx, name)
			if err != nil {
				fmt.Fprintln(stdout, errLine(err))
				return store.MapExit(err)
			}
			if exists {
				dups++ // 줄별 dedup: 이 줄만 skip, 나머지 신규 줄은 커밋된다
				continue
			}
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

	// emitDuplicate — 전부 duplicate(사전 확인·레이스 재확인 공통): 0건 커밋·
	// exit 0 "duplicate"(시딩 멱등). 유입 계측(T61)은 write 1회 + 실제 skip 줄
	// 수(이중 계수 금지), 경고는 폐기하지 않는다(dup 줄의 살균·비밀 경고 유지).
	emitDuplicate := func() {
		recordUsage(ctx, st, "write", source)
		recordDups(ctx, st, dups)
		out := store.OutputJSON("duplicate", "", "", 0)
		if len(dropped) > 0 || len(warnings) > 0 {
			out = withWarnings(out, dropped, warnings)
		}
		fmt.Fprintln(stdout, out)
	}

	if len(recs) == 0 {
		emitDuplicate()
		return 0
	}
	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		if !errors.Is(err, store.ErrDuplicate) {
			fmt.Fprintln(stdout, errLine(err))
			return store.MapExit(err)
		}
		// 레이스(사전 확인 통과 후 동시 삽입)로 배치가 롤백됐다: 생존 recs의
		// 고유 키(knowledge content_hash·project name)를 재확인해 실제 존재하는
		// 줄만 skip하고 잔여만 1회 재시도 — 전체 폐기 후 exit 0 "duplicate"는
		// 신규줄 유실이므로 금지.
		survive := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			if r["kind"] == "project" {
				// project 줄은 자연키 name 재확인(content_hash가 없어 재시도로
				// 판정하면 재충돌해 에러화된다 — 첫 시딩 실패의 사슬).
				name, _ := r["name"].(string)
				exists, err2 := st.ExistsProject(ctx, name)
				if err2 != nil {
					fmt.Fprintln(stdout, errLine(err2))
					return store.MapExit(err2)
				}
				if exists {
					dups++ // 동시 삽입된 줄: skip — 계측도 실제 skip 수만 반영
					continue
				}
				survive = append(survive, r)
				continue
			}
			h, _ := r["content_hash"].(string)
			if h == "" {
				survive = append(survive, r) // 해시 없는 kind는 재확인 불가 — 재시도로 판정
				continue
			}
			exists, err2 := st.ExistsHash(ctx, h)
			if err2 != nil {
				fmt.Fprintln(stdout, errLine(err2))
				return store.MapExit(err2)
			}
			if exists {
				dups++ // 동시 삽입된 줄: skip — 계측도 실제 skip 수만 반영
				continue
			}
			survive = append(survive, r)
		}
		if len(survive) == 0 {
			// 재확인 전부 존재: 신규줄 유실 없는 경로 — exit 0 "duplicate".
			emitDuplicate()
			return 0
		}
		ids, err = st.InsertBatch(ctx, survive)
		if err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				// 재시도까지 ErrDuplicate: 재확인을 통과한 해시의 실제 충돌 —
				// ErrDuplicate 자격을 벗겨 에러로 보고(유실 없는 경로만 exit 0).
				err = fmt.Errorf("write: batch conflict after dedup recheck: %v", err)
			}
			fmt.Fprintln(stdout, errLine(err))
			return store.MapExit(err)
		}
		recs = survive
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
		out = store.MultiCommitOutput(len(recs), dups, commitHash)
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
// UsageCountStore(실구현)는 count 1회 호출로 일괄 증분, 미구현 Store는 1회씩.
func recordDups(ctx context.Context, st Store, n int) {
	if n <= 0 {
		return
	}
	if cs, ok := st.(UsageCountStore); ok {
		_ = cs.RecordUsageCount(ctx, "dup", "", n)
		return
	}
	for i := 0; i < n; i++ {
		recordUsage(ctx, st, "dup", "")
	}
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
