// doctor.go — T64: flint doctor. brew doctor 준용 — 검사 5종을 돌아
// fail만 exit 1, warn은 0, fail마다 수정 명령 한 줄(fix)을 제시한다.
// 유지보수 경로(오프라인 CLI)라 핫패스에 간섭하지 않는다(§2).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stall grace 기본값(Healthchecks.io 준용) — T66이 Thresholds에
// stall_hot_h·stall_write_h 메타 오버라이드를 추가하면 그 체계로 흡수된다.
const (
	stallHotDefaultH   = 48
	stallWriteDefaultH = 72
)

// DoctorChecks — 검사 5종 고정 순서(스펙 §1).
func DoctorChecks() []string {
	return []string{"hook-file", "db", "schema", "integrity", "last-seen"}
}

func init() { Register("doctor", runDoctor) }

// doctorCheck — 개별 검사 결과. status = ok|warn|fail.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// doctorOutput — 한 줄 JSON 출력 모양. status는 결과 최악값.
type doctorOutput struct {
	Status string        `json:"status"`
	Checks []doctorCheck `json:"checks"`
}

// runDoctor — --list-checks·--check NAME 서브셋·기본 전체 검사.
// 무효 --check명은 exit 3(전역 계약: 무효 인자 3). fail 있으면 1, warn은 0.
func runDoctor(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	subset := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list-checks":
			b, _ := json.Marshal(map[string][]string{"checks": DoctorChecks()})
			fmt.Fprintln(stdout, string(b))
			return 0
		case "--check":
			i++
			if i >= len(args) {
				fmt.Fprintln(stdout, errLine(errors.New("doctor: --check requires a name")))
				return 1
			}
			subset = args[i]
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", args[i])))
			return 1
		}
	}
	names := DoctorChecks()
	if subset != "" {
		ok := false
		for _, n := range names {
			if n == subset {
				ok = true
				break
			}
		}
		if !ok {
			fmt.Fprintln(stdout, errLine(fmt.Errorf("doctor: unknown check %q", subset)))
			return 3
		}
		names = []string{subset}
	}

	checks := make([]doctorCheck, 0, len(names))
	for _, n := range names {
		checks = append(checks, runDoctorCheck(ctx, st, n))
	}
	overall := "ok"
	for _, c := range checks {
		switch c.Status {
		case "fail":
			overall = "fail"
		case "warn":
			if overall != "fail" {
				overall = "warn"
			}
		}
	}
	b, err := json.Marshal(doctorOutput{Status: overall, Checks: checks})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	if overall == "fail" {
		return 1
	}
	return 0
}

// runDoctorCheck — 검사명 디스패치.
func runDoctorCheck(ctx context.Context, st Store, name string) doctorCheck {
	switch name {
	case "hook-file":
		return checkHookFile()
	case "db":
		return checkDB(ctx, st)
	case "schema":
		return checkSchema(ctx, st)
	case "integrity":
		return checkIntegrity(ctx, st)
	case "last-seen":
		return checkLastSeen(ctx, st)
	}
	return doctorCheck{Name: name, Status: "fail", Detail: "unknown check"}
}

// claudeHome — 훅 설정 디렉터리. FLINT_CLAUDE_HOME 오버라이드 우선.
func claudeHome() string {
	if h := os.Getenv("FLINT_CLAUDE_HOME"); h != "" {
		return h
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, ".claude")
}

// checkHookFile — 훅 설정(settings.json)에 flint 등록 여부.
// 미등록은 적립 계측(write --source hook)이 누락되는 fail.
func checkHookFile() doctorCheck {
	c := doctorCheck{Name: "hook-file"}
	home := claudeHome()
	path := filepath.Join(home, "settings.json")
	if home == "" {
		c.Status, c.Fix = "fail", "fix: FLINT_CLAUDE_HOME 또는 HOME을 설정해 훅 설정 경로를 지정"
		return c
	}
	b, err := os.ReadFile(path)
	if err != nil {
		c.Status = "fail"
		c.Detail = path + " 없음"
		c.Fix = "fix: settings.json에 flint 훅 등록 — flint-plugin/hooks/hooks.json 참조"
		return c
	}
	if !containsFlintHook(string(b)) {
		c.Status = "fail"
		c.Detail = path + "에 flint 훅 없음"
		c.Fix = "fix: settings.json hooks에 flint write 훅 추가 — flint-plugin/hooks/hooks.json 참조"
		return c
	}
	c.Status = "ok"
	c.Detail = path
	return c
}

// containsFlintHook — 설정 본문에 flint 훅 명령 흔적이 있는지(단순 포함 검사).
func containsFlintHook(body string) bool { return strings.Contains(body, "flint") }

// checkDB — Ping.
func checkDB(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "db"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint setup — docker compose up -d"
		return c
	}
	if err := st.Ping(ctx); err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint setup — docker compose up -d"
		return c
	}
	c.Status = "ok"
	return c
}

// checkSchema — meta schema_version == 4(v0.6 계측 컬럼 승격 확인).
func checkSchema(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "schema"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — 스키마 승격 확인"
		return c
	}
	rows, err := queryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — DB 접속·스키마 확인"
		return c
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if version != "4" {
		c.Status = "fail"
		c.Detail = "schema_version=" + version
		c.Fix = "fix: v0.6 CLI로 재접속해 스키마 승격 — flint status"
		return c
	}
	c.Status = "ok"
	return c
}

// checkIntegrity — flint_knowledge_* 인덱스 3행(v3 필터 인덱스).
func checkIntegrity(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "integrity"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — 인덱스 승격 확인"
		return c
	}
	rows, err := queryOnce(ctx, st,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'knowledge' AND indexname LIKE 'flint_knowledge_%'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — DB 접속·인덱스 확인"
		return c
	}
	n := -1
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &n)
	}
	if n < 3 {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("flint_knowledge_* 인덱스 %d개(기대 3)", n)
		c.Fix = "fix: flint status — v3 인덱스 승격 재시도"
		return c
	}
	c.Status = "ok"
	return c
}

// checkLastSeen — 3경로 last_* 경과 + grace(nodata 패턴).
// hot 48h·write 72h 초과 또는 기록 부재(never)는 warn — exit를 깨지 않는다.
func checkLastSeen(ctx context.Context, st Store) doctorCheck {
	c := doctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — DB 접속"
		return c
	}
	rows, err := queryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn" // 접속 불량은 stall 오탐 방지를 위해 warn(조용히)
		c.Detail = "last_* 조회 실패: " + err.Error()
		return c
	}
	seen := map[string]time.Time{}
	for _, r := range rows {
		if len(r) != 2 || r[0] == nil || r[1] == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(r[1]))
		if err != nil {
			continue // 파손 값은 없는 취급(무효 값 폴백)
		}
		seen[fmt.Sprint(r[0])] = ts
	}
	warns := 0
	if detail := stallDetail(seen, "last_hot_at", stallHotDefaultH, &warns); detail != "" {
		c.Detail = detail
	}
	if detail := stallDetail(seen, "last_write_at", stallWriteDefaultH, &warns); detail != "" {
		if c.Detail != "" {
			c.Detail += "; "
		}
		c.Detail += detail
	}
	if warns > 0 {
		c.Status = "warn"
		return c
	}
	c.Status = "ok"
	return c
}

// stallDetail — 키의 경과가 grace를 넘으면 경고 문구 반환(부재=never).
func stallDetail(seen map[string]time.Time, key string, graceH int, warns *int) string {
	ts, ok := seen[key]
	if !ok {
		*warns++
		return key + " 기록 없음(never)"
	}
	if d := time.Since(ts); d > time.Duration(graceH)*time.Hour {
		*warns++
		return fmt.Sprintf("%s 경과 %s(grace %dh 초과)", key, d.Round(time.Hour), graceH)
	}
	return ""
}
