// done.go — flint done: Stop 훅 종료 경로. 미기록 연속 경고만 내고
// 종료를 절대 막지 않는다(PLAN §13 비간섭 5원칙 — 어떤 경로든 exit 0).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"flint/lib/backup"
	"flint/lib/db"
	"flint/lib/store"
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

// doneThresholds — loadThresholds(maintain.go 소유) 재사용. 읽기 실패 시
// 기본값 폴백(비간섭: 경고 경로가 본말을 바꾸지 않는다).
func doneThresholds(ctx context.Context, st Store) store.Thresholds {
	th, err := loadThresholds(ctx, st)
	if err != nil {
		return store.DefaultThresholds()
	}
	return th
}

// agentForSession — session 값으로 집계 대상 agent를 찾는다. 숫자면
// sessions.id 조회 결과를 쓰고, 아니거나 미발견이면 값을 그대로 agent로
// 쓴다(훅이 넘기는 세션 식별자 형식이 섞여 있어 둘 다 수용).
func agentForSession(ctx context.Context, st Store, session string) string {
	if id, err := strconv.Atoi(session); err == nil {
		rows, err := queryOnce(ctx, st, `SELECT agent FROM sessions WHERE id = $1`, []any{id})
		if err == nil && len(rows) > 0 {
			if a, ok := rows[0][0].(string); ok && a != "" {
				return a
			}
		}
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
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			session = args[i+1]
			i++
		}
	}
	if session == "" {
		// 사용법 오류도 종료를 막지 않는다 — 한 줄 JSON, exit 0(§13).
		fmt.Fprintln(stdout, `{"error":"usage: flint done --session <id>"}`)
		return 0
	}
	th := doneThresholds(ctx, st)
	runs, err := staleRuns(ctx, st, agentForSession(ctx, st, session))
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 0
	}
	if runs >= th.StaleSessions {
		fmt.Fprintf(stdout, `{"status":"stale","runs":%d,"warning":"연속 %d세션 미기록"}`+"\n", runs, runs)
		autoBackupFn(ctx)
		return 0
	}
	fmt.Fprintln(stdout, `{"status":"ok"}`)
	autoBackupFn(ctx)
	return 0
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
	auto := backup.Auto{DSN: dsnFromEnv()}
	if err := auto.RunIfNeeded(ctx,
		func(key string) (string, error) {
			var v string
			err := pool.QueryRow(ctx,
				`SELECT value FROM meta WHERE key = $1`, key).Scan(&v)
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil
			}
			return v, err
		},
		func(key, value string) error {
			_, err := pool.Exec(ctx,
				`INSERT INTO meta (key, value) VALUES ($1, $2)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
			return err
		},
		lf); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup auto: %v\n", err)
	}
}

// lazyDumpFile — 첫 Write 시에만 덤프 파일을 만드는 지연 writer.
// RunIfNeeded가 비경과 no-op일 때 빈 파일을 남기지 않게 한다.
type lazyDumpFile struct {
	dir    string
	once   sync.Once
	f      *os.File
	failed bool
}

func (l *lazyDumpFile) Write(p []byte) (int, error) {
	l.once.Do(func() {
		if err := os.MkdirAll(l.dir, 0o755); err != nil {
			l.failed = true
			return
		}
		name := filepath.Join(l.dir,
			"flint-"+time.Now().UTC().Format("20060102-150405")+".sql")
		f, err := os.Create(name)
		if err != nil {
			l.failed = true
			return
		}
		l.f = f
	})
	if l.failed {
		return 0, errors.New("backup auto: dump 파일 생성 불가")
	}
	return l.f.Write(p)
}

func (l *lazyDumpFile) close() {
	if l.f != nil {
		l.f.Close()
	}
}

func init() {
	Register("done", runDone)
}
