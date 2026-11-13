// hot.go — flint hot: 주입용 요약. 형식 고정 + 상한은 meta hot_budget(기본 2,000).
// v0.39.0 T3: lib/cli/hot.go 이동(동작·출력·exit 불변). 세션 라벨 파서·
// 노출 프로파일 판독·hot 통계 로드/persist·출력 길이 기록은 cli 잔류
// 소유라 주입 seam(*Fn var) 경유 — cli init(knowledge_alias.go)이 실구현을
// 주입한다(사이클 방지: knowledge는 cli 임포트 불가).
package knowledge

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// DefaultHotBudget — meta hot_budget 미설정 시 기본 상한(TUNING.md §1).
const DefaultHotBudget = 2000

// SessionLabelFn — 인자에서 --session 값을 추출하는 공유 파서 주입 점
// (cli sessionhook.go sessionLabel). cli init이 실구현을 주입한다.
var SessionLabelFn func(args []string) string

// ExposureProfileFn — 노출 프로파일 판독 주입 점(cli profile.go
// ExposureProfile). cli init이 실구현을 주입한다.
var ExposureProfileFn func(getKV func(string) (string, error)) string

// RecordHotLenFn — hot 출력 글자수 기록 주입 점(cli metrics.go
// RecordHotLen). cli init이 실구현을 주입한다.
var RecordHotLenFn func(n int)

// LoadHotStatsFromMetaFn — hot_stats 누적 로드 주입 점(cli metrics.go).
// cli init이 실구현을 주입한다.
var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

// PersistHotStatsToMetaFn — hot_stats 누적 persist 주입 점(cli metrics.go).
// cli init이 실구현을 주입한다.
var PersistHotStatsToMetaFn func(ctx context.Context, st wire.Store)

// HotBudget — meta hot_budget 우선, 미설정·무효 값은 기본 2,000(에러 아님).
func HotBudget(getKV func(string, string) (string, error)) int {
	v, err := getKV("hot_budget", "")
	if err != nil || v == "" {
		return DefaultHotBudget
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return DefaultHotBudget
	}
	return n
}

// MetaGetKV — meta 키 조회를 getKV 형태로 감싼다(미설정·오류는 def).
// v0.34.3 T2: 질의 본체는 meta_kv 단일 진입(wire.GetMetaKVErr)에 위임.
func MetaGetKV(ctx context.Context, st wire.Store) func(string, string) (string, error) {
	return func(key, def string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return def, err
		}
		return v, nil
	}
}

// HotFields — hot 출력 컬럼(포맷·예산 생존 판정이 공유하는 고정 순서).
// HotSQL SELECT의 앞 4컬럼과 대응한다 — FormatRows는 kind·ts를 위치
// fallback, topic·summary를 readColumns 매핑(위치 1·2)으로 읽으므로
// 말미의 id·valid_at·ingested_at 증분 컬럼이 출력에 끼지 않는다.
const HotFields = "kind,topic,summary,ts"

// HotSQL SELECT 말미의 증분·접미 소비 컬럼 인덱스(T5 단일 정의 —
// hot_view가 노출하는 id·valid_at·ingested_at을 본질의에서 직접 읽는다).
const (
	hotViewIDIdx         = 4
	hotViewValidAtIdx    = 5
	hotViewIngestedAtIdx = 6 // 팔별 확인용(현재 미소비 — 뷰 노출과 쌍)
)

// hotLineSurvivorCount — 행별 '표시 문자열' 기준 생존 수(N4). --full 접미
// (·+N일)가 붙은 최종 텍스트로 잰다: k행 시작 오프셋은 lines[:k]의 rune
// 수(행 구분 줄바꿈 포함)와 같고, 그 오프셋이 budget 미만인 행만 살아남는다.
// ClampHot이 절단하는 실출력과 같은 문자열을 재므로, 접미가 밀어낸 행이
// 실출력에서 잘렸는데도 생존으로 세어 증분되는 부정합이 없다.
func hotLineSurvivorCount(lines []string, budget int) int {
	n, off := 0, 0
	for i, ln := range lines {
		if i > 0 {
			off++ // 행 구분 줄바꿈
		}
		if off >= budget {
			break
		}
		off += utf8.RuneCountInString(ln)
		n++
	}
	return n
}

// injectedIDsForSurvivors — 실제 주입된 지식 id를 hot_view 본질의 행에서
// 직접 모은다(T5 단일 정의): 생존 survivors행 중 project 팔(id NULL)을
// 제외한 행의 id 셀(hotViewIDIdx)을 취한다. 주입 출력과 증분 원천이 같은
// 조회·같은 순서(ORDER BY ts DESC, id DESC tie-break)라 주입 집합=증분
// 집합이 구조적으로 성립한다 — 세 팔 조건을 다시 모은 동반 질의와의
// 정합 계약을 폐지하고도 동등하다. id 셀 부재(구형 뷰·fake)·변환 불가는
// 조용히 건너뛴다(잘린 행의 id는 증분하지 않는다).
func injectedIDsForSurvivors(rows [][]any, survivors int) []int {
	injected := make([]int, 0, survivors)
	for i := 0; i < survivors && i < len(rows); i++ {
		r := rows[i]
		if len(r) <= hotViewIDIdx || fmt.Sprint(r[0]) == "project" {
			continue
		}
		if id, ok := render.CellToInt(r[hotViewIDIdx]); ok {
			injected = append(injected, id)
		}
	}
	return injected
}

// PendingAgeSuffix — pending 팔 대기일수 접미(v0.12.1 R2): valid_at 경과
// 24시간 미만은 빈 문자열, 이상은 내림 일수 "·+N일".
func PendingAgeSuffix(validAt time.Time, now time.Time) string {
	if days := int64(now.Sub(validAt) / (24 * time.Hour)); days > 0 {
		return fmt.Sprintf("·+%d일", days)
	}
	return ""
}

// hotSuffixedLines — --full 전체 출력의 행별 표시 문자열(접미 포함, N4).
// ClampHot 절단 대상이 되는 실출력 텍스트와 증분 생존 판정
// (hotLineSurvivorCount)이 같은 문자열을 기준으로 정합하도록 출력 조립과
// 생존 산정이 공유한다. pending 행 끝에만 대기일수 접미를 붙인다 —
// valid_at은 hot_view 본질의 컬럼(hotViewValidAtIdx, T5 단일 정의)에서
// 직접 읽는다(동반 질의 폐지). 셀 부재·time.Time 아닌 값·줄바꿈 포함
// (안전 가드)은 접미 생략해 기본 포맷 라인을 반환한다(기존 출력 불변
// 승계). rows가 비면 nil. now는 UTC.
func hotSuffixedLines(rows [][]any, now time.Time) []string {
	base := query.FormatRows(rows, HotFields, false)
	if base == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(base, "\n"), "\n")
	hasPending := false
	for _, r := range rows {
		if len(r) > 0 && fmt.Sprint(r[0]) == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return lines
	}
	// 안전 가드: 필드 값에 줄바꿈이 포함되면 FormatRows 한 행이 여러 line으로
	// 쪼개져 lines[i]↔rows[i] 대응이 어긋난다 — 접미가 잘못된 행에 붙지 않도록
	// 행 수가 다르면 접미 전체를 생략한다(기존 출력 불변 승계).
	if len(lines) != len(rows) {
		return lines
	}
	for i, r := range rows {
		if i >= len(lines) {
			break
		}
		if len(r) <= hotViewValidAtIdx || fmt.Sprint(r[0]) != "pending" {
			continue
		}
		if va, ok := r[hotViewValidAtIdx].(time.Time); ok {
			lines[i] += PendingAgeSuffix(va, now)
		}
	}
	return lines
}

// hotFullOutput — 행별 표시 문자열(접미 포함)을 --full 전체 출력 텍스트로
// 조립한다. nil(빈 rows)은 기존 빈 출력("") 승계.
func hotFullOutput(lines []string) string {
	if lines == nil {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// HotHeadline — hot 요약 헤드라인 1줄(R2). minimal은 활성·대기 카운트만,
// 그 외 프로파일(미설정·파손 포함)은 normal 문구.
// v0.18.0 R2: normal은 최근 결정 주제 K개 + 회상 힌트 — 주입이 카운트만
// 주면 다음 세션이 뭘 검색할지 몰라 read가 굶는다(관측: read:write 불균형).
// 주제 없는 세션은 기존 카운트+펼치기 문구로 폴백(문구 열거는 데이터 —
// 펼치기 안내와 같은 클래스, §13 경계 준수).
func HotHeadline(profile string, active int, pending int, recent int, topics []string) string {
	if profile == "minimal" {
		return fmt.Sprintf("[flint] 활성 %d · 대기 %d", active, pending)
	}
	if len(topics) > 0 {
		return fmt.Sprintf("[flint] 활성 %d · 대기 %d · 최근 결정: %s — 회상: flint read <쿼리>",
			active, pending, strings.Join(topics, " · "))
	}
	return fmt.Sprintf("[flint] 활성 %d · 대기 %d · 최근 결정 %d — 펼치기: flint hot --full",
		active, pending, recent)
}

// hotHeadlineTopicMax — 헤드라인 주제 rune 상한(절산 시 … 접미).
const hotHeadlineTopicMax = 16

// hotHeadlineTopicCount — 헤드라인에 나열할 주제 수(상위 K).
const hotHeadlineTopicCount = 3

// HotTopics — hot_view 조회 행(최신순)에서 decision 주제 상위 K를 뽑는다.
// 동일 주제 중복 제거(최신 1건만)·rune 절산. 주제는 검색 큐리로 쓰이므로
// 원문 접두를 보존한다(16 rune + …).
func HotTopics(rows [][]any, k int) []string {
	if k <= 0 {
		return nil
	}
	topics := make([]string, 0, k)
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r) < 2 || fmt.Sprint(r[0]) != "decision" {
			continue
		}
		topic := strings.TrimSpace(fmt.Sprint(r[1]))
		if topic == "" || seen[topic] {
			continue
		}
		seen[topic] = true
		topics = append(topics, clampHeadlineTopic(topic))
		if len(topics) == k {
			break
		}
	}
	return topics
}

// clampHeadlineTopic — 주제 rune 상한 절산(초과 시 … 접미).
func clampHeadlineTopic(s string) string {
	runes := []rune(s)
	if len(runes) <= hotHeadlineTopicMax {
		return s
	}
	return string(runes[:hotHeadlineTopicMax]) + "…"
}

// hotCounts — hot_view 조회 행(전체 출력과 동일 데이터 소스)에서
// 헤드라인 카운트를 뽑는다: 활성=projects 팔, 대기=pending, 최근 결정=7일.
func hotCounts(rows [][]any) (active, pending, recent int) {
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		switch fmt.Sprint(r[0]) {
		case "project":
			active++
		case "pending":
			pending++
		case "decision":
			recent++
		}
	}
	return active, pending, recent
}

// RunHot — hot_view 조회 후 --full이면 기존 전체 출력(바이트 불변),
// 아니면 프로파일별 헤드라인 1줄만 출력한다(카운트는 동일 조회에서 추출,
// 전체 조립은 생략). 계측·exit·--session은 양 경로 공통 불변.
// T38 배선(F-T56-1): 시작 시 meta hot_stats 누적을 로드하고 종료 직전
// persist한다(단발 CLI 프로세스 분리 보존). 둘 다 계측이므로 실패는
// 조용히 무시된다(§13 — hot 출력·exit에 영향 금지).
func RunHot(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// 세션 라벨은 공유 파서(SessionLabelFn — cli sessionLabel 주입)로 —
	// session-start 경계 기록과 단일 소스(v0.20.0 리뷰 #7). --full 판별은
	// 세션 값으로 소비된 토큰을 플래그로 재판독하지 않는다(변칙 입력
	// `--session --full`의 기존 full=true 부작용 제거 — read의 플래그형 값
	// 방어와 같은 방향). v1.0.1(F3): --session 라벨은 read와 같은 공용 가드
	// (validateSessionLabel)로 검증 — 빈 값·'-' 접두 오타는 무효 인자 exit 3.
	session := SessionLabelFn(args)
	sessionGiven := false
	full := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			sessionGiven = true
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if args[i] == "--full" {
			full = true
		}
	}
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	sql := query.HotSQL(session)
	if !full {
		// v0.20.1(F3): 헤드라인 경로는 카운트 왜곡 방지 상한(50) —
		// 주입(--full)은 예산 상한 계약 불변.
		sql = query.HotHeadlineSQL(session)
	}
	rows, err := wire.QueryOnce(ctx, st, sql, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// 출력 재살균(ADR-0006, v0.20.6 패치 루프 6차 S1): read와 동일
	// 방어 — 저장 후 유입(restore·직접 SQL·살균 이전 데이터)된 [flint]
	// 마커·제어 문자가 세션 시작 주입 경계를 위조하지 않게 한다. hot은
	// 훅으로 프롬프트에 직접 주입되는 최우선 출력 경로다. v0.37.0: 살균은
	// read와 같은 render 조크포인트(render.Cells)로 수렴.
	rows = render.Cells(rows)
	// 조회 성공 후 기록 직전에 baseline 로드(쿼리 실패 경로는 계측 생략).
	LoadHotStatsFromMetaFn(ctx, st)
	// 예산은 양 경로 공통 산출: --full 절단 규칙과 증분 생존 판정이 같은
	// 예산을 공유한다(계측 불변).
	budget := HotBudget(MetaGetKV(ctx, st))
	// 접미 포함 행별 텍스트(N4) — --full 출력과 증분 생존 판정이 공유한다.
	var lines []string
	if full {
		lines = hotSuffixedLines(rows, time.Now().UTC())
		clamped := query.ClampHot(hotFullOutput(lines), budget)
		fmt.Fprint(stdout, clamped)
		RecordHotLenFn(len([]rune(clamped)))
		// G5 바이트 계측(v1.1.0 T1): ClampHot 절단 '후' 최종 출력 길이
		// (예산 준수 실측 — 절단 전 아님, spec §1). 조용히(§13).
		recordBytes(ctx, st, "hot", len(clamped))
	} else {
		kv := MetaGetKV(ctx, st)
		profile := ExposureProfileFn(func(key string) (string, error) { return kv(key, "") })
		active, pending, recent := hotCounts(rows)
		line := HotHeadline(profile, active, pending, recent, HotTopics(rows, hotHeadlineTopicCount))
		fmt.Fprintln(stdout, line)
		RecordHotLenFn(len([]rune(line)))
		// G5 바이트 계측: 헤드라인 1줄+개행 — 쓴 총량(분모 정합). 조용히(§13).
		recordBytes(ctx, st, "hot", len(line)+1)
	}
	PersistHotStatsToMetaFn(ctx, st)
	// 계측(T62): 실제 주입된(예산 절단 후 출력에 남은) 지식 id만 참조 증분
	// + hot 유입 1회. 조용히(§13). 지식 주입은 --full 전체 출력 경로에서만
	// 일어난다(N3) — 헤드라인 경로는 카운트만 노출하므로 실제 주입 없는
	// 증분(read_count·last_read_at 왜곡으로 stale 판정 동결) 없이 유입 1회만
	// 기록한다. 생존 판정은 접미 포함 텍스트 기준(N4): 접미가 밀어낸 행이
	// 실출력에서 잘렸으면 증분하지 않는다. 증분 id는 본질의 hot_view 조회의
	// id 컬럼에서 직접 뽑는다(T5 단일 정의) — 동반 질의·그 실패 경로는 폐지.
	if !full {
		RecordUsage(ctx, st, "hot", "")
		// v1.2.0 T2: 헤드라인(비 --full) 노출 id 전수도 회상 이력에
		// kind=hot로 남긴다(spec §2.1 — 주입은 --full 경로뿐이지만 헤드라인
		// 도 회상을 유도하는 노출 표면이라 G4 계정에 포함). id는 헤드라인
		// 질의의 id 컬럼에서 회수한다(HotSQL 단일 정의 관례 — 프로젝트 팔
		// id NULL 제외, 셀 부재·변환 불가는 조용히 건너뛴다). read_count
		// 캐시(BumpReads)는 헤드라인 경로에서 계속 불볕한다.
		recordKnowledgeReads(ctx, st, injectedIDsForSurvivors(rows, len(rows)), session, "hot")
	} else {
		injected := injectedIDsForSurvivors(rows, hotLineSurvivorCount(lines, budget))
		InstrumentReads(ctx, st, "hot", injected, false, false)
		// v1.2.0 T2: 주입 id 전수도 회상 이력에 kind=hot로(주입=증분=기록
		// 집합 — 같은 조회·같은 순서 원천). 조용히(§13).
		recordKnowledgeReads(ctx, st, injected, session, "hot")
		// v0.20.0(E): 실제 주입된 지식 id를 hot_events에 노출 기록 —
		// dead_pct의 hot 노출 보정 원천. 실패는 조용히(§13 계측 원칙).
		if rec, ok := st.(HotEventRecorder); ok {
			_ = rec.RecordHotEvent(ctx, idsCSV(injected))
		}
	}
	return 0
}

// HotEventRecorder — hot --full 주입 노출 기록 접합면(v0.20.0).
// realStore는 db.Repo.RecordHotEvent로 구현(cli/realstore_read.go),
// 미구현 Store(fake)는 건너뛴다.
type HotEventRecorder interface {
	RecordHotEvent(ctx context.Context, ids string) error
}
