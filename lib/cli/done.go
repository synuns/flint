// done.go — flint done: Stop 훅 종료 경로. 미기록 연속 경고만 내고
// 종료를 절대 막지 않는다(PLAN §13 비간섭 5원칙 — 어떤 경로든 exit 0).
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
)

// staleSessionStore — T17 StaleSessionRuns 접근. Store 인터페이스는
// v0.1 소유라 확장 대신 타입 단언으로 소비한다(root.go 수정 금지).
type staleSessionStore interface {
	StaleSessionRuns(ctx context.Context, agent string) (int, error)
}

// realStore 위임 메서드 — 같은 패키지 파일이라 여기서 추가만 한다.
func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

// usageStore — T37 LogPlaybookUse 접근. Store 인터페이스는 v0.1 소유라
// 확장 대신 타입 단언으로 소비한다(root.go 수정 금지).
type usageStore interface {
	LogPlaybookUse(ctx context.Context, id int, ok bool) error
}

func (s *realStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	return s.repo.LogPlaybookUse(ctx, id, ok)
}

// logPlaybookUse — 종료 직전 사용 로그 1건(T37). --playbook으로 받은 id가
// 있을 때만 기록하며 done 출력·exit code는 완전 불변, 실패는 stderr 1행
// 이하로 조용히 무시한다(비간섭 §13 — maybeAutoBackup과 같은 격).
func logPlaybookUse(ctx context.Context, st Store, id int, ok bool) {
	if id <= 0 {
		return
	}
	us, can := st.(usageStore)
	if !can {
		return
	}
	if err := us.LogPlaybookUse(ctx, id, ok); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] playbook use log: %v\n", err)
	}
}

// DoneResult — done 판정 결과(세션 자동 요약 재료).
type DoneResult struct {
	Agent      string // 집계 대상 agent
	Unrecorded int    // 미기록 연속 세션 수
	// v0.27.0 R1: 종료 요약(세션 지식화 소스) — Compact 결과 요약이
	// 전달되지 않으면 종료 상태 요약으로 폴백.
	Summary string
}

// sessionEndPrefix — 종료 요약 마커 접두(세션 갱신 대상 식별용).
const sessionEndPrefix = "세션 종료"

// SessionRecord — sessions upsert용 레코드(kind·agent·종료 요약·빈 tasks).
func (d *DoneResult) SessionRecord() map[string]any {
	return map[string]any{
		"kind":    "session",
		"agent":   d.Agent,
		"summary": fmt.Sprintf("%s(미기록 %d)", sessionEndPrefix, d.Unrecorded),
		"tasks":   "[]",
	}
}

// writeSessionRecord — 세션 자동 요약 upsert(T41, 코디네이터 룰링 a):
// 같은 agent의 최근 세션 행을 종료 요약으로 갱신해 세션 수를 불변으로
// 유지한다(신규 행이 StaleSessionRuns 미기록 카운트를 오염시키는 결함
// 해소 — T44 인시던트). agent의 세션이 없을 때만 INSERT. sessions 갱신은
// maintain 갱신 경로 준용. 실패는 stderr 1행 이하로 조용히 무시(비간섭 §13).
func writeSessionRecord(ctx context.Context, st Store, rec map[string]any) {
	agent, _ := rec["agent"].(string)
	// v0.26.2(재검수 발견2): agent 라벨 살균·비밀 스캔 — InsertBatch 직접
	// 호출이라 sanitizePayload를 우회하던 사각([flint] 마커·비밀 무검사
	// 저장). 경고만(저장은 계약대로 계속 — §13).
	for _, name := range store.ScanSecrets(agent) {
		fmt.Fprintf(os.Stderr, "[flint] session agent: possible secret: %s\n", name)
	}
	agent = store.SanitizeText(agent)
	rec["agent"] = agent
	rows, err := queryOnce(ctx, st,
		`SELECT id FROM sessions WHERE agent = $1
		 ORDER BY ts DESC, id DESC LIMIT 1`, []any{agent})
	if err == nil && len(rows) > 0 && rows[0][0] != nil {
		// pgx는 int4를 int32로 스캔한다 — 타입 단언 대신 문자열 경유 파싱.
		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); n == 1 {
			if _, err := queryOnce(ctx, st,
				`UPDATE sessions SET summary = $1, tasks = $2 WHERE id = $3`,
				[]any{rec["summary"], rec["tasks"], id}); err != nil {
				fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
			}
			return
		}
	}
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
	}
}

// doneThresholds — loadThresholds(maintain.go 소유) 재사용. 읽기 실패 시
// 기본값 폴백(비간섭: 경고 경로가 본말을 바꾸지 않는다).
func doneThresholds(ctx context.Context, st Store) store.Thresholds {
	th, err := loadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
}

// hookDefaultAgent — UUID 세션의 집계 agent 기본값. 훅 실배선은 Claude
// session_id(UUID)를 --session으로 넘기는데, UUID를 그대로 agent로 쓰면
// 세션마다 agent가 갈라져 StaleSessionRuns가 영구히 1을 못 넘어 stale
// 경고가 발화되지 않는다(T17/T44 도달성 결함). compact 적립 행의 agent
// (compact.go "claude")와 같은 하니스 식별자로 안정시킨다.
const hookDefaultAgent = "claude"

// hookSessionUUIDRe — 훅 실배선이 넘기는 Claude session_id 형식
// (8-4-4-4-12 hex). claude 흡수는 이 형식에만 해당한다(2차 리뷰 #1).
var hookSessionUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// agentForSession — session 값으로 집계 대상 agent를 찾는다. 숫자면
// sessions.id 조회 결과를 쓰고, UUID면 하니스 기본 agent(hookDefaultAgent,
// 훅 UUID 세션이 compact 행과 한 agent로 집계). 그 외 미해석 값(e2e·타
// 하니스 문자 세션, 미발견 숫자 id)은 그 값 자체를 agent로 쓴다 —
// 흡수하면 실제 claude 세션 summary를 덮어쓰고 stale 경고가 오발화된다.
func agentForSession(ctx context.Context, st Store, session string) string {
	if id, err := strconv.Atoi(session); err == nil {
		rows, err := queryOnce(ctx, st, `SELECT agent FROM sessions WHERE id = $1`, []any{id})
		if err == nil && len(rows) > 0 {
			if a, ok := rows[0][0].(string); ok && a != "" {
				return a
			}
		}
	}
	if hookSessionUUIDRe.MatchString(session) {
		return hookDefaultAgent
	}
	return session
}

// staleRuns — Store가 StaleSessionRuns를 미구현(레거시 fake)이면
// 경고 없는 0이 안전한 기본값이다.
func staleRuns(ctx context.Context, st Store, agent string) (int, error) {
	if ss, ok := st.(staleSessionStore); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil
}

// runDone — `flint done --session <id>`: 미기록 연속 >= StaleSessions면
// stale 경고, 그 외 ok. 경고일 뿐 종료를 막지 않는다(항상 exit 0).
func runDone(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	playbook, playbookOK := 0, true // --playbook <id>·--playbook-ok <bool>(기본 성공)
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
		}
		if args[i] == "--playbook" && i+1 < len(args) {
			// 최근 read가 참조한 플레이북 id를 훅이 넘긴다(T37 사용 로그).
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				playbook = n
			}
			i++
		}
		if args[i] == "--playbook-ok" && i+1 < len(args) {
			if b, err := strconv.ParseBool(args[i+1]); err == nil {
				playbookOK = b
			}
			i++
		}
	}
	if session == "" {
		// 사용법 오류도 종료를 막지 않는다 — 한 줄 JSON, exit 0(§13).
		fmt.Fprintln(stdout, `{"error":"usage: flint done --session <id>"}`)
		return 0
	}
	th := doneThresholds(ctx, st)
	agent := agentForSession(ctx, st, session)
	runs, err := staleRuns(ctx, st, agent)
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 0
	}
	res := &DoneResult{Agent: agent, Unrecorded: runs}
	if runs >= th.StaleSessions {
		fmt.Fprintf(stdout, `{"status":"stale","runs":%d,"warning":"연속 %d세션 미기록"}`+"\n", runs, runs)
		writeSessionRecord(ctx, st, res.SessionRecord())
		logPlaybookUse(ctx, st, playbook, playbookOK)
		autoBackupFn(ctx)
		return 0
	}
	// v0.18.0 R3: 회상 경고 — 오늘 회상 0회에 기록이 임계 이상이면 한 줄
	// 경고로 종료(stale 경고와 같은 패턴 — 임계값 기반·종료 불가, §13).
	// v0.18.1: Stop 훅 stdout은 자동 배선에서 안 보이므로 recall 마커도
	// 남긴다 — 다음 SessionStart(nudge)가 1회 표시 후 소진한다.
	// v0.18.2: 별도 파일(recall-hint.json) — 증류 대기 마커와 덮어쓰기
	// 충돌 방지(수명 주기 상이: 1회성 vs 내구).
	if w, starved := recallStarvedToday(ctx, st, th); starved {
		fmt.Fprintf(stdout, `{"status":"recall","writes":%d,"warning":"오늘 회상 0회에 기록 %d회 — 다음 세션은 작업 전 flint read부터"}`+"\n", w, w)
		// v0.24.0 R2a: 마커와 병행 meta 카운터 적립 — minimal 프로파일이
		// 마커를 조용히 종결해도 doctor가 프로파일 무관 관측한다.
		bumpMetaCounter(ctx, st, "recall_starved_count")
		if merr := WriteTypedMarker(markerKindRecall, session, nudgeReasonRecall); merr != nil {
			fmt.Fprintf(os.Stderr, "[flint] recall marker: %v\n", merr)
		}
	} else {
		fmt.Fprintln(stdout, `{"status":"ok"}`)
	}
	writeSessionRecord(ctx, st, res.SessionRecord())
	maybeSessionKnowledge(ctx, st, res, agent, session)
	logPlaybookUse(ctx, st, playbook, playbookOK)
	autoBackupFn(ctx)
	return 0
}

// maybeSessionKnowledge — v0.27.0 R1(세션 지식화): 세션 종료 시 세션
// 요약에서 지식 후보를 pending knowledge로 적립한다. 규칙 기반만
// (LLM 판정 배제 — §13·조사 확정): 최소 길이·중복(content_hash dedup)·
// 빈도 상한(임계 session_knowledge_max_days로 일 1회)을 지닌 요약만.
// 적립은 pending(검수 대기) — approve 경로로 승격된다(신뢰 게이트 승계).
func maybeSessionKnowledge(ctx context.Context, st Store, res *DoneResult, agent, session string) {
	th := writeThresholds(ctx, st)
	summary := strings.TrimSpace(res.Summary)
	// v0.27.0 리뷰 1: 실제 요약(Compact 결과 등)이 전달된 경우만 적립 —
	// 폴백 상용구("세션 종료(미기록 N)")는 지식이 아니므로 스킵.
	// 전달 경로(표준화) 활성 전까지 사실상 대기 상태: 마커·임계 회로는
	// 유지되고 Summary가 채워지는 순간부터 자동 작동한다.
	if summary == "" || len([]rune(summary)) < 12 {
		return
	}
	if th.SessionKnowledgeDays <= 0 {
		return // -1 = 끔
	}
	// 일 빈도 상한 — 마지막 적립이 N일 이내면 skip(연속 세션 스팸 방지).
	getKV, _ := metaKV(ctx, st)
	if v, err := getKV("session_knowledge_last"); err == nil && v != "" {
		if last, perr := time.Parse(time.RFC3339, v); perr == nil &&
			time.Since(last) < time.Duration(th.SessionKnowledgeDays)*24*time.Hour {
			return
		}
	}
	rec := map[string]any{
		"kind":       "knowledge",
		"topic":      fmt.Sprintf("세션[%s]", agentLabel(agent, 24)),
		"summary":    store.SanitizeText(summary),
		"tags":       ":process:",
		"domain":     "main",
		"confidence": 0.5, // pending 게이트 — approve 경로 승격
		"status":     "pending",
	}
	rec = store.Normalize("knowledge", rec)
	rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
	if exists, err := st.ExistsHash(ctx, rec["content_hash"].(string)); err == nil && exists {
		return // 중복 방지(같은 세션 재종료 재적립 차단)
	}
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		return // 조용히 — 비간섭(§13): 지식화 실패가 done 종료를 깨지 않는다
	}
	// 마커 적립 — 증류 회로(nudge 예산 문구)로 자연 연결(R2).
	_ = WriteTypedMarker(markerKindDistill, session, "session-knowledge")
	_, setKV := metaKV(ctx, st)
	if setKV != nil {
		_ = setKV("session_knowledge_last", time.Now().UTC().Format(time.RFC3339))
	}
}

// agentLabel — 세션 라벨(빈 값 폴백 + truncateRunesImport 재사용 —
// 리뷰 9: 절단 헬퍼 단일화).
func agentLabel(agent string, max int) string {
	if agent == "" {
		return "unknown"
	}
	return truncateRunesImport(agent, max)
}

// recallStarvedToday — 오늘 daily 카운터로 회상 부재 판정(v0.18.0 R3).
// read 0 && write >= RecallWarnWrites일 때만 true. 카운터 조회·파손은
// 조용히 false(경고 부재 = 종료 경로 불변 — §13).
func recallStarvedToday(ctx context.Context, st Store, th store.Thresholds) (writes int, starved bool) {
	if th.RecallWarnWrites <= 0 {
		return 0, false
	}
	getKV, _ := metaKV(ctx, st)
	v, err := getKV("daily:" + time.Now().UTC().Format("2006-01-02"))
	if err != nil || v == "" {
		return 0, false
	}
	var d reportDay
	if json.Unmarshal([]byte(v), &d) != nil {
		return 0, false
	}
	return d.Write, d.Read == 0 && d.Write >= th.RecallWarnWrites
}

// autoBackupFn — seam. 기본 실동작, fast 단위 테스트는 no-op으로 교체
// (실동작 검증은 e2e ④가 실DB·실덤프로 한다).
var autoBackupFn = maybeAutoBackup

// maybeAutoBackup — 하루 1회 자동 백업(spec §8 "하루 첫 세션 지연 트리거").
// done 판정 뒤에만 실행하며 출력·exit code는 완전 불변, 실패는 stderr 1행
// 이하로 조용히 무시한다(비간섭 §13 — 코디네이터 룰링 T24 ④).
func maybeAutoBackup(ctx context.Context) {
	dir := os.Getenv("FLINT_BACKUP_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".flint", "backup")
	}
	pool, err := db.Connect(ctx, dsnFromEnv())
	if err != nil {
		return
	}
	defer pool.Close()

	lf := &lazyDumpFile{dir: dir}
	defer lf.close()
	metaGet := func(key string) (string, error) {
		var v string
		err := pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, key).Scan(&v)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return v, err
	}
	auto := backup.Auto{DSN: dsnFromEnv()}
	runErr := auto.RunIfNeeded(ctx,
		metaGet,
		func(key, value string) error {
			_, err := pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
			return err
		},
		lf)
	// v0.20.1(패치 루프 1차 F4): 자동 백업도 로테이션 — 파일 확정(사이드카
	// 포함) 뒤 keep-N 정책을 적용한다. 이전엔 수동 `flint backup --out`
	// 경로(rotateAfterBackup)에만 묶여 있어 자동 파일이 무한히 축적됐다.
	// 실패는 경고만(백업 성공 불변 — 수동 경로와 동일 계약).
	if runErr == nil {
		if name := lf.closeAndPath(); name != "" {
			if _, rerr := rotateFn(dir, keepFn(metaGet)); rerr != nil {
				fmt.Fprintf(os.Stderr, "[flint] backup auto rotate: %v\n", rerr)
			}
		}
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup auto: %v\n", runErr)
	}
}

// lazyDumpFile — 첫 Write 시에만 덤프 파일을 만드는 지연 writer.
// RunIfNeeded가 비경과 no-op일 때 빈 파일을 남기지 않게 한다.
// 기록 바이트는 sha256 tee로 합산되고 close가 .sha256 사이드카를
// 남긴다(R3 — ADR-0006 무결 검증을 자동 파일에도 적용).
type lazyDumpFile struct {
	dir    string
	once   sync.Once
	f      *os.File
	h      hash.Hash
	failed bool
}

func (l *lazyDumpFile) Write(p []byte) (int, error) {
	l.once.Do(func() {
		if err := os.MkdirAll(l.dir, 0o700); err != nil { // v0.20.6 S3: 덤프는 민감 평문 — 0700
			l.failed = true
			return
		}
		name := filepath.Join(l.dir,
			"flint-"+time.Now().UTC().Format("20060102-150405")+".sql")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // v0.20.6 S3
		if err != nil {
			l.failed = true
			return
		}
		l.f = f
		l.h = sha256.New()
	})
	if l.failed {
		return 0, errors.New("backup auto: dump 파일 생성 불가")
	}
	// v0.25.4(미커버 스윕 U2): 본문 기록 실패 시 해시 갱신도 중단하고
	// failed 마킹 — 부분 파일이 '내용과 일치하는' 정상 사이드카를 받아
	// 온전한 구 백업을 rotate에서 밀어내던 오탐 상태를 차단한다.
	n, err := l.f.Write(p)
	if err != nil {
		l.failed = true
		return n, err
	}
	l.h.Write(p)
	return n, nil
}

// close — 덤프 close 성공 후 .sha256 사이드카 기록. once 미발동(0바이트
// no-op)이면 파일·사이드카 모두 만들지 않는다. 사이드카 실패는 경고 로그
// 후 진행(비차단 — done 반환·exit 불변).
func (l *lazyDumpFile) close() error {
	if l.f == nil {
		return nil
	}
	name := l.f.Name()
	closeErr := l.f.Close()
	l.f = nil // 멱등 — closeAndPath 이후 defer close의 이중 close 방어
	// v0.25.4(U2): 본문 기록 실패한 부분 파일은 사이드카 없이 제거 —
	// 체크섬 통과하는 오탐 백업 슬롯을 남기지 않는다. 이중 close
	// 에러(실패 유도로 이미 닫힌 경우)보다 폐기가 우선.
	if l.failed {
		_ = os.Remove(name)
		return errors.New("backup auto: 부분 덤프 폐기")
	}
	if closeErr != nil {
		return closeErr
	}
	sum := hex.EncodeToString(l.h.Sum(nil))
	if err := backup.WriteSidecar(name, sum); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup sidecar: %v\n", err)
		return fmt.Errorf("sidecar: %w", err)
	}
	return nil
}

// closeAndPath — 파일을 확정(close·사이드카)하고 경로를 반환한다.
// 기록이 없던 no-op면 빈 문자열. v0.20.1: 자동 백업 로테이션이 확정된
// 경로를 얻는 접합(이후 defer close는 l.f=nil로 no-op).
func (l *lazyDumpFile) closeAndPath() string {
	if l.f == nil {
		return ""
	}
	name := l.f.Name()
	if err := l.close(); err != nil {
		return ""
	}
	return name
}

func init() {
	Register("done", runDone)
}

// bumpMetaCounter — meta 카운터 1 증분(done 회상 카운터 전용 유틸).
// 실패는 조용히(§13 — 계측 원칙).
func bumpMetaCounter(ctx context.Context, st Store, key string) {
	_, _ = queryOnce(ctx, st,
		`INSERT INTO meta (key, value) VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = (meta.value::bigint + 1)::text`, []any{key})
}
