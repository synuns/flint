// read.go — flint read [query]: active_knowledge 뷰 강제 조회 + 토큰 예산.
// v0.39.0 T3: lib/cli/read.go 이동(동작·출력·exit 불변). as-of 스펙 파서는
// cli asof.go 소유라 주입 seam(ParseAsOfSpecFn·ErrInvalidAsOf) 경유 —
// cli init이 실구현을 주입한다(사이클 방지: knowledge는 cli 임포트 불가).
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// ParseAsOfSpecFn — as-of 스펙 → 커밋 해시 정규화의 주입 점(cli asof.go
// ParseAsOfSpec). cli init(knowledge_alias.go)이 실구현을 주입한다.
var ParseAsOfSpecFn func(ctx context.Context, st wire.Store, spec string) (string, error)

// ErrInvalidAsOf — 무효 as-of 스펙 판정 errors.Is 원천 주입 점(cli asof.go
// ErrInvalidAsOf와 동일 인스턴스를 cli init이 주입한다).
var ErrInvalidAsOf error

// readBumper — v0.6 항목 참조 계측 seam(T62 BumpReads).
type readBumper interface {
	BumpReads(ctx context.Context, ids []int) error
}

// ReadEventStore — 효과 계측 이벤트 1행 기록 접합면(v0.19.0,
// docs/METRICS.md §4). realStore는 db.Repo.RecordReadEvent로 구현하고
// (cli/realstore_read.go), 미구현 Store(fake)는 이 계측을 건너뛴다(카운터
// 폴백과 달리 대체 경로가 없다 — read_events 없이도 나머지 지표는
// daily·knowledge에서 도출된다). v0.20.0: ids(반환 지식 id CSV, 상위 10)
// 추가 — 사용 연쇄(chain) 산출 원천. v0.42.0 T3: 인자 산재 호출을
// db.ReadEvent 구조체 1값으로 수렴(동작 불변).
type ReadEventStore interface {
	RecordReadEvent(ctx context.Context, ev db.ReadEvent) error
}

// BytesRecorder — G5 바이트 계측 접합면(v1.1.0, spec §1). realStore는
// db.Repo.RecordBytes로 구현(cli/realstore_read.go), 미구현 Store(fake)는
// 이 계측을 건너뛴다(ReadEventStore 소비 패턴 승계).
type BytesRecorder interface {
	RecordBytes(ctx context.Context, kind string, n int) error
}

// KnowledgeReadsRecorder — 항목별 회상 이력 기록 접합면(v1.2.0 T2, spec
// §2.1 G4 정합·§2.6 회상 운명). realStore는 db.Repo.RecordKnowledgeReads로
// 구현하고, 미구현 Store(fake)는 이 계측을 건너뛴다(ReadEventStore 소비
// 패턴 승계). read 반환 id 전수(kind=read)·hot 주입/헤드라인 노출 id
// 전수(kind=hot)를 knowledge_reads 테이블에 남긴다 — read_count 캐시
// (BumpReads)와 별개의 이력 계층이다.
type KnowledgeReadsRecorder interface {
	RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error
}

// recordKnowledgeReads — 회상 이력 기록(계측 — 실패는 조용히, §13).
func recordKnowledgeReads(ctx context.Context, st wire.Store, ids []int, session, kind string) {
	if kr, ok := st.(KnowledgeReadsRecorder); ok {
		_ = kr.RecordKnowledgeReads(ctx, ids, session, kind)
	}
}

// defaultZeroOwnedFloor — 제로히트 분해 임계 기본값(spec §2.2: active≥1이면
// owned_miss). meta 오버라이드(zero_owned_floor)는 per-key 직접 읽기
// stopgap(등록표 키 등록 전 — n>0만 수용. v1.2.3 T9로 동일 관례의
// knowledge_reads_retention_days는 등록표 경유로 수렴됨)로 읽는다.
const defaultZeroOwnedFloor = 1

// zeroOwnedFloorKey — 제로히트 분해 임계 meta 오버라이드 키.
const zeroOwnedFloorKey = "zero_owned_floor"

// zeroOwnedFloor — 제로히트 분해 임계값(부재·파손·0 이하는 기본값 — 조용히 §13).
func zeroOwnedFloor(ctx context.Context, st wire.Store) int {
	if v, ok := wire.GetMetaKV(ctx, st, zeroOwnedFloorKey); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return defaultZeroOwnedFloor
}

// activeKnowledgeCount — 현재 시점 활성 지식 수(제로히트 분해 판정 원천,
// spec §2.2). 승인은 invalid_at을 청소하므로 status='active' AND
// invalid_at IS NULL이 활성의 단일 정의다. 조회 실패·빈 결과는 0(조용히 §13).
func activeKnowledgeCount(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	if n, ok := render.CellToInt(rows[0][0]); ok {
		return n
	}
	return 0
}

// ZeroReason — 제로히트 분해 순수 함수(spec §2.2 CRAG): zero=1 && 활성
// 지식 수 < floor → "unowned"(사실상 미보유), zero=1 && ≥floor →
// "owned_miss"(보유탐색실패), zero=0 → ""(분해 대상 아님). 쿼리 본문은
// 기록하지 않는다(개인 문구 부재 원칙).
func ZeroReason(zero bool, active, floor int) string {
	if !zero {
		return ""
	}
	if active < floor {
		return "unowned"
	}
	return "owned_miss"
}

// recordBytes — 표면 출력 직후 len(최종 출력)을 kind 바이트로 기록한다
// (UTF-8 바이트 — io.Writer에 쓴 총량). 미구현 no-op, 실패는 조용히
// (§13 계측 원칙 — 바이트 로그 유실이 호출 실패보다 낫다).
func recordBytes(ctx context.Context, st wire.Store, kind string, n int) {
	if br, ok := st.(BytesRecorder); ok {
		_ = br.RecordBytes(ctx, kind, n)
	}
}

// idsCSV — 정수 id 슬라이스를 CSV로(저장 예산 상한 10 — hit 전수는
// hits 컬럼이 유지한다). v0.20.0 chain 산출 원천의 직렬화.
func idsCSV(ids []int) string {
	if len(ids) > 10 {
		ids = ids[:10]
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

// FirstExactRank — 반환 행에서 첫 topic 정합(lower 일치, RankOrder
// 정의 승계)의 1-기반 순위. 정합 없음 0(v0.30.0 R2 — 품질 근사 원천).
func FirstExactRank(rows [][]any, q string) int {
	// v0.31.3(감사 4): RankOrder 원천은 SQL lower(원 q) — trim 없음.
	// 품질 기록도 동일 정의를 쓴다(공백 포함 q에서 순위 왜곡 방지).
	lq := strings.ToLower(q)
	if lq == "" {
		return 0 // 빈 쿼리(전체 조회)는 정합 개념 없음
	}
	for i, r := range rows {
		if len(r) < 2 {
			continue
		}
		if strings.ToLower(fmt.Sprint(r[1])) == lq { // 열 1 = topic
			return i + 1
		}
	}
	return 0
}

// ParseIDsCSV — idsCSV 역직렬화(report 소비용). 빈 문자열·빈 칸은
// 공란, 파싱 불가 칸은 조용히 건너뛴다(계측 관례).
func ParseIDsCSV(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// RowIDs — 조회 결과 행에서 id 컬럼(위치 0)을 int로 모은다. 매핑 불가
// 칸은 조용히 건너뛴다(계측 누락은 조회를 깨지 않는다). 셀 변환은
// render.CellToInt 단일 구현(v0.39.0 T3 부채 수렴).
func RowIDs(rows [][]any) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if id, ok := render.CellToInt(row[0]); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// InstrumentReads — 참조 계측(T62): 결과 항목 id 전수 증분 + 유입 1회
// 기록. v0.42.0 T2: read kind의 유입 계측(daily read 카운터·read_stats
// blob)은 폐지 — 계측 원천이 read_events(RecordReadEvent)로 일원화돼
// read 1회 쓰기가 4→2(BumpReads·RecordReadEvent)로 줄었다. zero·near
// 분류는 RecordReadEvent 인자로만 전달된다(서명 호환을 위해 파라미터는
// 유지). 그 외 kind(hot)는 기존 RecordUsage 경로 불변. 계측이라 실패는
// 조용히 무시된다.
func InstrumentReads(ctx context.Context, st wire.Store, kind string, ids []int, zero, near bool) {
	if rb, ok := st.(readBumper); ok {
		_ = rb.BumpReads(ctx, ids)
	}
	if kind == "read" {
		return // 유입 계측은 read_events 단일 원천(v0.42.0 T2)
	}
	RecordUsage(ctx, st, kind, "")
}

// RunRead — 무료 텍스트 query와 --tag --entity --domain --limit 필터.
// --kind는 active_knowledge가 knowledge 단일 kind 뷰라 의미가 없어 받지
// 않는다. --status pending|active|invalidated(v0.12.0 R1) — 지정 시에만
// base 테이블 knowledge + status WHERE 조회(뷰는 invalidated 행을
// 노출하지 않는다).
func RunRead(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	f := query.Filters{}
	jsonMode, fields, asOfSpec, statusSpec, session, sourceSpec := false, "", "", "", "", ""
	asOfGiven, statusGiven, sessionGiven, sourceGiven := false, false, false, false
	tagGiven, entityGiven, domainGiven, fieldsGiven := false, false, false, false
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		// 표준 관례(N9): "--" 이후 전부 위치 인자 — MCP 등 외부 입력의
		// query가 "--json"처럼 플래그처럼 보여도 플래그로 파싱하지 않는다.
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		var v string
		if i+1 < len(args) {
			v = args[i+1]
		}
		switch a {
		case "--tag":
			f.Tag, i, tagGiven = v, i+1, true
		case "--entity":
			f.Entity, i, entityGiven = v, i+1, true
		case "--domain":
			f.Domain, i, domainGiven = v, i+1, true
		case "--limit":
			n, err := strconv.Atoi(v)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "limit", Expect: "integer"}))
				return 3
			}
			f.Limit, i = n, i+1
		case "--status":
			statusSpec, i = v, i+1
			statusGiven = true
		case "--as-of":
			asOfSpec, i = v, i+1
			asOfGiven = true
		case "--json":
			jsonMode = true
		case "--fields":
			fields, i, fieldsGiven = v, i+1, true
		case "--session":
			// v0.19.0: 효과 계측용 세션 라벨(선택 — docs/METRICS.md §4).
			// 조회 동작에는 영향 없고 read_events 기록에만 쓰인다.
			session, i, sessionGiven = v, i+1, true
		case "--source":
			// v1.2.0 T2: 채널 라벨(선택 — read_events.source 계측). lib/mcp
			// 도구가 cli.Run을 재사용하므로 MCP 경로 구분은 호출부 전달로
			// 최소 배선한다(write --source 관례 승계). 조회 동작 불변.
			sourceSpec, i, sourceGiven = v, i+1, true
		default:
			pos = append(pos, a)
		}
	}
	q := strings.Join(pos, " ")
	// 값 없는 trailing --tag/--entity/--domain/--fields(N10)도 값 없는
	// --status/--as-of(F1)와 동일 규칙 exit 3 — 빈 값이 조용히 무시되면
	// 의도와 다른 필터로 결과를 돌려준다. --limit은 이미 Atoi 실패로 exit 3.
	// (--session의 빈 값·'-' 접두는 validateSessionLabel 공용 가드로 — v1.0.1
	// F3 승격, 아래.)
	for _, g := range []struct {
		given bool
		val   string
		field string
	}{
		{tagGiven, f.Tag, "tag"},
		{entityGiven, f.Entity, "entity"},
		{domainGiven, f.Domain, "domain"},
		{fieldsGiven, fields, "fields"},
	} {
		if g.given && g.val == "" {
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: g.field, Expect: "non-empty"}))
			return 3
		}
	}
	// v0.20.5(B②): --session 미지정 시 현재 세션 라벨 자동 채택
	// (session-start 훅이 기록 — G4 라벨 자동화, 조용히).
	if !sessionGiven {
		if label, ok := wire.ReadSessionLabel(); ok {
			session = label
		}
	}
	// --session 값은 read_events에 14일간 저장되는 라벨이다 — 다른 값형
	// 플래그와 달리 오타(`--session --json`처럼 플래그를 흡수)가 DB 오염으로
	// 남는다. 플래그형 값은 즉시 exit 3(리뷰 #3 — 조회 결과 오염 방지).
	// v1.0.1(F3): 가드는 validateSessionLabel 공용 구현으로 승격 — hot·done이
	// 같은 검증(exit 3)을 쓴다.
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	// v1.2.0 T2: source 라벨 확정(read_events.source — spec §2.3 채널 믹스).
	// 명시 --source(hook|cli|mcp)가 우선하고, 미지정 시 진입 시점 규칙 —
	// 세션 라벨이 있으면 'hook'(세션 훅 주입 경로), 없으면 'cli'. 무효값·
	// 값 없음은 무효 인자 exit 3(write --source 관례 승계 — 저장되는 라벨의
	// 오타가 DB 오염으로 남는 것을 막는다).
	source := sourceSpec
	if sourceGiven {
		switch source {
		case "hook", "cli", "mcp":
		case "":
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "non-empty"}))
			return 3
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "source", Expect: "hook|cli|mcp"}))
			return 3
		}
	} else if session != "" {
		source = "hook"
	} else {
		source = "cli"
	}
	// v0.20.2(패치 루프 1차 F5): --fields 이름은 알려진 컬럼만 허용 —
	// 오타("sumary")가 FormatRows의 위치 fallback으로 조용히 다른 컬럼
	// 값을 채우던 왜곡을 검증 에러로 바꾼다. hot의 kind·ts 등 read 밖
	// 필드는 hot이 --fields를 받지 않아 영향 없다.
	if fieldsGiven {
		for _, name := range strings.Split(fields, ",") {
			if name = strings.TrimSpace(name); name == "" || !query.ReadFieldKnown(name) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
					Field:  "fields",
					Expect: "알려진 컬럼: " + query.ReadColumnsHint(),
				}))
				return 3
			}
		}
	}
	asOfHash, asOfDate := "", ""
	// 값 없는 --as-of(마지막 인자)도 무효 스펙 exit 3 — 조용히 현재 시점
	// 데이터를 돌려주면 잘못된 결과가 되므로(status와 동일 규칙).
	if asOfGiven && asOfSpec == "" {
		fmt.Fprintln(stdout, asOfErrLine("invalid_as_of",
			fmt.Errorf("빈 as-of 스펙: %w", ErrInvalidAsOf).Error()))
		return 3
	}
	// 값 없는 --status(마지막 인자)도 무효 스펙 exit 3 — 값 없는 --as-of
	// 가드와 동일 규칙: 조용히 무시하면 의도와 다른 결과를 돌려준다. 무효
	// status 값 검증은 --as-of 지정 여부와 무관하게 항상 수행한다. --as-of
	// 병용 시 as-of 우선·status 무시(룰링 — 경고 없이)는 유효한 값에만
	// 적용되며, Filters.Status 반영은 단독 지정 시에만 한다.
	if statusGiven || statusSpec != "" {
		switch statusSpec {
		case "pending", "active", "invalidated":
			if asOfSpec == "" {
				f.Status = statusSpec
			}
		default:
			fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{
				Field: "status", Expect: "pending|active|invalidated"}))
			return 3
		}
	}
	var sql string
	var qargs []any
	if f.Status != "" {
		sql, qargs = query.BuildReadStatus(q, f)
	} else {
		sql, qargs = query.BuildRead(q, f)
	}
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			if errors.Is(err, ErrInvalidAsOf) {
				fmt.Fprintln(stdout, asOfErrLine("invalid_as_of", err.Error()))
				return 3
			}
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		// Doltgres 커밋 해시는 base32계 소문자([0-9a-z]) — ParseAsOfSpec이
		// 접두어 검증·유일 해시를 보장하므로 반환값을 그대로 신뢰해 인라인.
		// AS OF 절은 테이블 참조 바로 뒤(뷰+AS OF "현재 정의+과거 행"). 기존
		// RankOrder·LIMIT 절은 그대로 둔다.
		// v0.28.0(H③): 해시 값은 dolt_log 행 출처라 엔진 구현에 의존하지
		// 않게 엄격 문자셋 재검증 — 벗어나면 인라인 대신 오류(종료 계약).
		if !asOfHashRe.MatchString(hash) {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("as-of hash 문자셋 위반: %q", hash)))
			return 1
		}
		sql = strings.Replace(sql, " FROM active_knowledge",
			" FROM active_knowledge AS OF '"+hash+"'", 1)
		asOfHash = hash
		// 커밋 시각 첨부 — 메타데이터 조회 실패는 조용히(빈 값, §13).
		if dr, err := wire.QueryOnce(ctx, st,
			`SELECT date FROM dolt_log WHERE commit_hash = $1`, []any{hash}); err == nil &&
			len(dr) == 1 && len(dr[0]) > 0 {
			asOfDate = FmtAsOfDate(dr[0][0])
		}
	}
	rows, err := wire.QueryOnce(ctx, st, sql, qargs)
	if err != nil {
		// 뷰-컬럼 미호환(과거 행에 없는 컬럼)은 기본 테이블 조회 안내로 랩(C3).
		if asOfSpec != "" {
			err = wrapViewColumnMismatch(err)
		}
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// v0.9.6 제로히트 폴백: 구문 매칭 히트가 없을 때만 단어 OR 재쿼리 후
	// RRF 융합 재랭킹(룰링 — 구문 히트 경로는 완전 불변). v0.25.0:
	// ReorderByFallbackCount는 정합수·read_count·자모·topic 4신호 RRF.
	// rrf_k 오버라이드 주입을 재랭킹 **앞**으로(v0.25.1 패치 루프 — 이전엔
	// 뒤의 제안 블록에 있어 단발 CLI에서 같은 실행에 반영되지 않았다).
	// 1-토큰 질의는
	// 구문과 동질이라 생략. --as-of(과거 시점)는 현재 시점 폴백이 시점
	// 정합성을 깨므로 생략. --status는 상태 필터 자체가 재시도 대상이
	// 아니라 폴백·제안 미발동(룰링). 폴백 시도는 meta read_fallback_calls에
	// 1회 증분(조용히). 폴백도 제로히트면 rows 그대로(기존 제로히트 출력).
	// v0.19.0: 폴백 시도 여부를 read_events 기록에 전달(METRICS §4 G1).
	fallbackTried := false
	// F6(성능): 제로히트 경로의 thresholds 로드는 1회 — 아래 rrf_k 주입과
	// 제안 임계값 주입이 같은 실행에서 meta IN 쿼리를 2회 날리던 것을
	// 공유 1회로(로드 실패는 기본값 유지 — 조용히 §13, 기존과 동일).
	var th store.Thresholds
	thOK := false
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {
		// rrf_k 주입 hoist — 폴백 재랭킹이 같은 실행에서 오버라이드를
		// 반영한다(실패는 기본값 유지 — 조용히 §13).
		if loaded, thErr := wire.LoadThresholdsFn(ctx, st); thErr == nil {
			th, thOK = loaded, true
			query.SetRRFK(th.RrfK)
		}
		if fsql, fargs, ok := query.BuildReadFallback(q, f); ok {
			BumpReadFallbackToMeta(ctx, st)
			fallbackTried = true
			if frows, ferr := wire.QueryOnce(ctx, st, fsql, fargs); ferr == nil && len(frows) > 0 {
				rows = frows
				query.ReorderByFallbackCount(rows, query.TokenizeQuery(q))
			}
		}
	}
	// v0.14.0 R2 zero_near 판별: 구문·폴백 모두 제로히트 && 현재 시점일 때만
	// active_knowledge 전체 스캔(필터 무관)으로 근접 후보를 계산한다. v0.10.0
	// 제안 스캔을 출력 없이 승계 — 결과는 에이전트에게 보이지 않는 내부 분류
	// (zero_near 계측)에만 쓰인다. 스캔 실패·후보 0건은 조용히(§13).
	var suggestions []query.Suggestion
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {
		// 제안 임계값(min_score·scan_cap)은 thresholds 레지스트리 우선
		// (meta 오버라이드 → SetSuggestThresholds 주입, v0.16.3). 조회 실패는
		// query 패키지 기본값(=DefaultThresholds) 유지 — 조용히(§13).
		// F6(성능): 값은 위 폴백 블록의 1회 로드를 재사용(재조회 없음).
		if thOK {
			query.SetSuggestThresholds(th.SuggestMinScore, th.SuggestScanCap)
			query.SetRRFK(th.RrfK) // 이미 앞에서 주입 — 멱등 재확정(제안 경로)
		}
		ssql, sargs := query.BuildSuggestScan()
		if srows, serr := wire.QueryOnce(ctx, st, ssql, sargs); serr == nil {
			suggestions = query.SuggestSimilar(q, srows, 3)
		}
	}
	// 출력 중립화(ADR-0006): 저장 단계 살균을 읽기에도 재적용 — 저장 이후
	// 혹은 경로상 유입된 제어 문자·[flint] 마커가 출력 경계를 위조하지 않게
	// 한다. v0.37.0: 살균은 render 조크포인트(render.Cells)로 수렴.
	rows = render.Cells(rows)
	// v0.14.0 R1 침묵 계약: eligible(현재 시점·무상태필터) 제로히트는
	// 완전 무출력 — FormatRows도 빈 rows에 ""를 반환하므로 별도 분기가
	// 필요 없다. 제안 라인은 폐지(제로히트 결과 행 0 + 스캔 무출력).
	out := query.FormatRows(rows, fields, jsonMode)
	fmt.Fprint(stdout, out)
	// --as-of 지정 시 결과 뒤에 as_of{hash,commit_date} 한 줄(JSON) 추가.
	// 미지정이면 기존 필드만 그대로.
	byteLen := len(out) // G5(v1.1.0 T1): io.Writer에 쓴 총량(UTF-8 바이트)
	if asOfHash != "" {
		b, _ := json.Marshal(AsOfLine{AsOf: AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
		fmt.Fprintln(stdout, string(b))
		byteLen += len(b) + 1 // as_of 한 줄(+개행)도 쓴 총량에 포함
	}
	// G5 바이트 계측(v1.1.0 T1): read 1회 분 — 제로히트는 out="" 0바이트로
	// 기록된다(호출 수 분모 정합, spec §1). 조용히(§13).
	recordBytes(ctx, st, "read", byteLen)
	// 계측(T62): 결과 항목 id 전수 참조 증분. v0.42.0 T2 — read 유입의
	// read_stats blob·daily 카운터 쓰기는 폐지(쓰기 4→2: BumpReads·
	// RecordReadEvent만 남는다). v1.2.0 T2: id 전수는 knowledge_reads
	// 기록에도 재사용한다. 조용히(§13).
	rowIDs := RowIDs(rows)
	InstrumentReads(ctx, st, "read", rowIDs, len(rows) == 0, len(suggestions) > 0)
	// v1.2.0 T2: 반환 id 전수를 항목별 회상 이력(kind=read)으로 남긴다
	// (G4 정합·회상 운명 원천 — ids CSV의 상위 10 절사와 무관한 전수).
	// 조용히(§13).
	recordKnowledgeReads(ctx, st, rowIDs, session, "read")
	// 효과 계측(v0.19.0, docs/METRICS.md §4): read 1회를 read_events에
	// 기록. 미구현 Store(fake)는 건너뛰고 실패는 조용히(§13 계측 원칙).
	// v0.20.0: 반환 id CSV 동반(사용 연쇄 원천).
	if res, ok := st.(ReadEventStore); ok {
		// v0.30.0 R2: 첫 정합 순위 — 폴백 경유는 품질 지표에서 제외(0).
		// v0.31.3(감사 5): --as-of(과거 시점 검사)·--status(상태 필터
		// 조회)도 현재 검색 품질 지표에서 배제 — 품질 원천은 통상
		// 회상 경로만(METRICS §2 계약 문서화와 함께).
		rank := 0
		if !fallbackTried && asOfSpec == "" && f.Status == "" {
			rank = FirstExactRank(rows, q)
		}
		// v1.2.0 T2: 제로히트 행은 활성 지식 보유 여부로 unowned/owned_miss
		// 를 갈라 기록한다(spec §2.2 — zero=0 행은 ''). 조용히(§13).
		zeroReason := ""
		if len(rows) == 0 {
			zeroReason = ZeroReason(true, activeKnowledgeCount(ctx, st), zeroOwnedFloor(ctx, st))
		}
		_ = res.RecordReadEvent(ctx, db.ReadEvent{
			Hits:       len(rows),
			Zero:       len(rows) == 0,
			Near:       len(suggestions) > 0,
			Fallback:   fallbackTried,
			Session:    session,
			IDs:        idsCSV(rowIDs),
			FirstRank:  rank,
			Bytes:      byteLen,
			ZeroReason: zeroReason,
			Source:     source,
		})
	}
	// R1: eligible 제로히트는 exit 1(침묵 계약 — flint가 에이전트에게
	// 말을 걸지 않는다). --status·--as-of 경로는 기존 계약(exit 0) 불변.
	if ReadSuggestEligible(len(rows), asOfSpec, f.Status) {
		return 1
	}
	return 0
}

// validateSessionLabel — --session 라벨 공용 가드(v1.0.1 F3: read 계약
// 승격 — hot·done 동일 적용). given=false(플래그 미지정)은 통과. 빈 값
// (trailing --session)·'-' 접두(`--session --json`처럼 플래그를 흡수하는
// 오타 — 세션 라벨은 read_events·hot_view에 저장되는 값이라 오타가 DB
// 오염으로 남는다)·개행 포함(v1.0.3 R3: 세션 라벨 파일은 값 1줄 —
// \n·\r이 파일 계약과 저장값 양쪽을 깨뜨린다)은 무효 값 SchemaError —
// 호출부는 출력 후 exit 3.
func validateSessionLabel(given bool, val string) error {
	if !given {
		return nil
	}
	if val == "" {
		return &store.SchemaError{Field: "session", Expect: "non-empty"}
	}
	if strings.HasPrefix(val, "-") {
		return &store.SchemaError{Field: "session", Expect: "flag 아닌 라벨"}
	}
	if strings.ContainsAny(val, "\n\r") {
		return &store.SchemaError{Field: "session", Expect: "개행 없는 1줄 라벨"}
	}
	return nil
}

// ValidateSessionLabel — validateSessionLabel 내보내기(v1.0.2 R2-B1: 세션
// 훅 진입점 session-start·session-end가 라벨 기록·세션 시작 기록 앞에
// 쓰는 공용 가드 — 구현은 단일 소스 유지).
func ValidateSessionLabel(given bool, val string) error {
	return validateSessionLabel(given, val)
}

// ReadSuggestEligible — 폴백 게이트·제안 스캔 게이트·침묵 exit 판정이
// 공유하는 eligible 조건(3지점 복붙 통일): 현재 시점(--as-of 미지정)·
// 무상태필터(--status 미지정) 제로히트일 때만 참. 동작 불변.
func ReadSuggestEligible(rows int, asOfSpec, status string) bool {
	return rows == 0 && asOfSpec == "" && status == ""
}

// BumpReadFallbackToMeta — 폴백 시도 1회 증분(v0.9.6). 기존 계측 패턴
// (BumpReadStatsToMeta) 승계 — 실패는 조용히 무시(비간섭 §13).
// v0.34.3 T2: SELECT→INSERT 비원자 왕복을 bumpMetaKV 단일 원자
// upsert로 수렴(증분 중 동시 실행 유실 방지).
func BumpReadFallbackToMeta(ctx context.Context, st wire.Store) {
	_ = wire.BumpMetaKV(ctx, st, query.ReadFallbackCountKey)
}

// AsOfInfo / AsOfLine — --as-of 결과 첨부 한 줄 JSON {as_of:{hash,commit_date}}.
type AsOfInfo struct {
	Hash       string `json:"hash"`
	CommitDate string `json:"commit_date"`
}

type AsOfLine struct {
	AsOf AsOfInfo `json:"as_of"`
}

// asOfErrLine — {error,message} 한 줄 JSON 조립(ErrLine의 국소 변형 —
// wire.ErrLine은 store 계층 에러만 분류한다).
func asOfErrLine(code, msg string) string {
	b, _ := json.Marshal(map[string]string{"error": code, "message": msg})
	return string(b)
}

// FmtAsOfDate — dolt_log date 셀을 RFC3339 문자열로. time.Time이 아니면
// 그대로 문자열화한다.
func FmtAsOfDate(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

// wrapViewColumnMismatch — 뷰-컬럼 미호환(column ... does not exist) 오류에
// asOfHashRe — v0.28.0(H③): AS OF 인라인 해시의 엄격 문자셋(소문자
// base32계 — dolt_log commit_hash 규격). 엔진 반환값 신뢰의 이중 확인.
var asOfHashRe = regexp.MustCompile(`^[0-9a-z]{6,64}$`)

// ValidAsOfHash — AS OF 인라인용 해시의 asOfHashRe 문자셋 검증 노출
// (v0.28.0 H③와 동일 규칙 — 단일 소스). read 외 인라인 호출자(ops
// status 카운트 질의)가 같은 규칙으로 재검증하게 한다.
func ValidAsOfHash(hash string) bool {
	return asOfHashRe.MatchString(hash)
}

// 기본 테이블 조회 안내를 붙인다. 뷰+AS OF는 "현재 정의 + 과거 행" 규칙
// (C3)이라 과거 커밋에 없는 컬럼에서 이 오류가 난다. 그 외 오류는 불변.
func wrapViewColumnMismatch(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "column") && strings.Contains(msg, "does not exist") {
		return fmt.Errorf("%w — active_knowledge 뷰 정의가 해당 시점 행과 호환되지 않습니다. 기본 테이블(knowledge) AS OF 조회를 시도하세요", err)
	}
	return err
}
