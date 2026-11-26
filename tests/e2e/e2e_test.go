//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/mcp"
	"github.com/synuns/flint/lib/sqlfs"
)

func dsn() string {
	return testengine.ResolveDSN()
}

func mustDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	syncDSN := dsn() + "?default_query_exec_mode=simple_protocol"
	if strings.Contains(dsn(), "?") {
		syncDSN = dsn() + "&default_query_exec_mode=simple_protocol"
	}
	pool, err := db.Connect(context.Background(), syncDSN)
	if err != nil {
		if os.Getenv("FLINT_REALDB_REQUIRED") == "1" {
			t.Fatalf("전용 테스트 엔진 접속 실패(FLINT_REALDB_REQUIRED=1): %v", err)
		}
		t.Skipf("Doltgres 미기동: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(),
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 드롭 실패: %v", err)
	}
	for _, stmt := range statements(sqlfs.ViewsSQL()) {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("뷰 재생성 실패: %v", err)
		}
	}
	return pool
}

func statements(sqlText string) []string {
	var out []string
	for _, stmt := range strings.Split(sqlText, ";") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "--") {
				continue
			}
			lines = append(lines, l)
		}
		if s := strings.TrimSpace(strings.Join(lines, "\n")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanupTopics(t *testing.T, pool *pgxpool.Pool, prefix string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM knowledge WHERE topic LIKE $1`, prefix+"%"); err != nil {
		t.Fatalf("정리 실패(%s): %v", prefix, err)
	}
}

func countTopic(t *testing.T, pool *pgxpool.Pool, topic string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE topic = $1`, topic).Scan(&n); err != nil {
		t.Fatalf("count 실패: %v", err)
	}
	return n
}

func runCLI(t *testing.T, args []string, stdin string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("출력이 JSON 아님: %q: %v", out, err)
	}
	return m
}

func TestDedupIdenticalPayloadTwice(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-dedup-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-dedup-") })

	payload := `{"topic":"e2e-dedup-k","summary":"dedup 멱등 프로브","confidence":0.9,"tags":["e2e"]}`

	out1, code1 := runCLI(t, []string{"write", "knowledge"}, payload)
	if code1 != 0 {
		t.Fatalf("1회째 exit=%d, want 0; out=%s", code1, out1)
	}
	if m := decode(t, out1); m["status"] != "committed" {
		t.Fatalf("1회째 status=%v, want committed; out=%s", m["status"], out1)
	}

	out2, code2 := runCLI(t, []string{"write", "knowledge"}, payload)
	if code2 != 0 {
		t.Fatalf("2회째 exit=%d, want 0(중복도 성공); out=%s", code2, out2)
	}

	if m := decode(t, out2); m["status"] != "duplicate" {
		t.Fatalf("2회째 status=%v, want duplicate; out=%s", m["status"], out2)
	}

	if n := countTopic(t, pool, "e2e-dedup-k"); n != 1 {
		t.Fatalf("행 수=%d, want 1(멱등 위반)", n)
	}
}

func TestRetryRecoversAfterTransientCommitFailure(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-retry-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-retry-") })

	backend := backendAddr(t)
	proxy := newKillProxy(t, backend)
	t.Setenv("FLINT_DSN", "postgresql://flint:"+dbPassword(t)+"@"+proxy.Addr().String()+dsnPath(t))

	payload := `{"topic":"e2e-retry-k","summary":"재시도 회복 프로브","confidence":0.9}`
	start := time.Now()
	out, code := runCLI(t, []string{"write", "knowledge"}, payload)
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("exit=%d, want 0(재시도 후 성공); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}

	if elapsed < 900*time.Millisecond {
		t.Fatalf("경과 %v: 재시도 지연(0s/1s/3s) 경유 증거 부족", elapsed)
	}
	if n := countTopic(t, pool, "e2e-retry-k"); n != 1 {
		t.Fatalf("행 수=%d, want 1", n)
	}
}

func backendAddr(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(dsn())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	return u.Host
}

func dsnPath(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(dsn())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	return u.Path
}

type killProxy struct {
	ln     net.Listener
	mu     sync.Mutex
	killed bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func newKillProxy(t *testing.T, backend string) *killProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("프록시 리슨: %v", err)
	}
	p := &killProxy{ln: ln, conns: make(map[net.Conn]struct{})}
	t.Cleanup(func() {
		ln.Close()
		p.mu.Lock()
		for c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.serve(c, backend)
			}()
		}
	}()
	return p
}

func (p *killProxy) Addr() net.Addr { return p.ln.Addr() }

func (p *killProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

func (p *killProxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	c.Close()
}

func (p *killProxy) serve(c net.Conn, backend string) {
	p.track(c)
	s, err := net.Dial("tcp", backend)
	if err != nil {
		p.untrack(c)
		return
	}
	p.track(s)
	done := make(chan struct{})
	go func() { p.pipe(s, c, false); close(done) }()
	p.pipe(c, s, true)
	<-done
	p.untrack(c)
	p.untrack(s)
}

func (p *killProxy) pipe(src, dst net.Conn, scan bool) {
	defer src.Close()
	defer dst.Close()
	var carry []byte
	buf := make([]byte, 8192)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			data := buf[:n]
			if scan {
				carry = append(carry, data...)
				if bytes.Contains(carry, []byte("dolt_add")) {
					p.mu.Lock()
					first := !p.killed
					p.killed = true
					p.mu.Unlock()
					if first {
						return
					}
					carry = nil
				}
				if len(carry) > 16 {
					carry = carry[len(carry)-16:]
				}
			}
			if _, werr := dst.Write(data); werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

func TestReadEscapesLikeWildcards(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-esc-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-esc-") })

	jsonl := strings.Join([]string{
		`{"topic":"e2e-esc-k1","summary":"progress 100% done","confidence":0.9}`,
		`{"topic":"e2e-esc-k2","summary":"progress 100x done","confidence":0.9}`,
		`{"topic":"e2e-esc-k3","summary":"snake_case name","confidence":0.9}`,
		`{"topic":"e2e-esc-k4","summary":"snakexcase name","confidence":0.9}`,
		`{"topic":"e2e-esc-k5","summary":"o'brien quote","confidence":0.9}`,
	}, "\n")
	out, code := runCLI(t, []string{"write", "knowledge"}, jsonl)
	if code != 0 {
		t.Fatalf("씨딩 exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}

	assertRead := func(q, wantTopic string, wantLines int) {
		t.Helper()
		out, code := runCLI(t, []string{"read", q}, "")
		if code != 0 {
			t.Fatalf("read %q exit=%d, want 0; out=%s", q, code, out)
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) != wantLines {
			t.Fatalf("read %q 결과 %d건, want %d건; out=%s", q, len(lines), wantLines, out)
		}
		if wantTopic != "" && !strings.Contains(lines[0], wantTopic) {
			t.Fatalf("read %q 첫 줄에 %q 없음: %s", q, wantTopic, lines[0])
		}
	}

	assertRead("100%", "e2e-esc-k1", 1)
	assertRead("snake_case", "e2e-esc-k3", 1)
	assertRead("o'brien", "e2e-esc-k5", 1)

	outInj, codeInj := runCLI(t, []string{"read", `%' OR '1'='1`}, "")
	if codeInj != 0 {
		t.Fatalf("주입 시도 exit=%d, want 0(제로히트 — v1.5.3); out=%s", codeInj, outInj)
	}
	if outInj != "" {
		t.Fatalf("주입 시도 출력 있음(침묵 계약 위반): %q", outInj)
	}
}

func setMeta(t *testing.T, pool *pgxpool.Pool, key, value string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		t.Fatalf("meta 설정 실패(%s): %v", key, err)
	}
}

func delMeta(t *testing.T, pool *pgxpool.Pool, key string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key = $1`, key); err != nil {
		t.Fatalf("meta 삭제 실패(%s): %v", key, err)
	}
}

func writeKnowledge(t *testing.T, payload string) map[string]any {
	t.Helper()
	out, code := runCLI(t, []string{"write", "knowledge"}, payload)
	if code != 0 {
		t.Fatalf("write exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("write status=%v, want committed; out=%s", m["status"], out)
	}
	return m
}

func readLines(t *testing.T, query string) []string {
	t.Helper()
	out, code := runCLI(t, []string{"read", query}, "")
	if code != 0 {
		t.Fatalf("read %q exit=%d, want 0; out=%s", query, code, out)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestApprovePromotesPendingToActive(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-appr-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-appr-") })

	m := writeKnowledge(t,
		`{"topic":"e2e-appr-k","summary":"승격 프로브","confidence":0.7}`)
	if m["state"] != "pending" {
		t.Fatalf("state=%v, want pending", m["state"])
	}
	id := int(m["id"].(float64))

	readStatus := func() string {
		t.Helper()
		out, code := runCLI(t, []string{"read", "e2e-appr-k", "--json", "--fields", "id,status"}, "")
		if code != 0 {
			t.Fatalf("read exit=%d, want 0; out=%s", code, out)
		}
		m := decode(t, out)
		s, _ := m["status"].(string)
		return s
	}
	if s := readStatus(); s != "pending" {
		t.Fatalf("승인 전 read status=%q, want pending", s)
	}

	out, code := runCLI(t, []string{"approve", "--id", strconv.Itoa(id)}, "")
	if code != 3 {
		t.Fatalf("approve 무claim exit=%d, want 3(claim 게이트); out=%s", code, out)
	}
	out, code = runCLI(t, []string{"approve", "--id", strconv.Itoa(id), "--claim", "decision"}, "")
	if code != 0 {
		t.Fatalf("approve --claim exit=%d, want 0; out=%s", code, out)
	}
	if am := decode(t, out); am["status"] != "approved" || am["id"] != float64(id) {
		t.Fatalf("approve 출력=%v, want approved/%d", am, id)
	}

	if s := readStatus(); s != "active" {
		t.Fatalf("승인 후 read status=%q, want active", s)
	}
}

func TestMaintainCheckThresholdBoundary(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mnt-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mnt-") })

	var base int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`).
		Scan(&base); err != nil {
		t.Fatalf("baseline pending 수 실패: %v", err)
	}
	setMeta(t, pool, "pending_max", strconv.Itoa(base+2))
	t.Cleanup(func() { delMeta(t, pool, "pending_max") })
	setMeta(t, pool, "dup_susp_max", "1000000")
	t.Cleanup(func() { delMeta(t, pool, "dup_susp_max") })

	writeKnowledge(t, `{"topic":"e2e-mnt-k1","summary":"경계 1","confidence":0.7}`)
	writeKnowledge(t, `{"topic":"e2e-mnt-k2","summary":"경계 2","confidence":0.7}`)
	out, code := runCLI(t, []string{"maintain", "--check"}, "")
	if code != 0 {
		t.Fatalf("maintain --check exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("경계(== max) status=%v, want ok; out=%s", m["status"], out)
	}

	writeKnowledge(t, `{"topic":"e2e-mnt-k3","summary":"경계 3","confidence":0.7}`)
	out, code = runCLI(t, []string{"maintain", "--check"}, "")
	if code != 0 {
		t.Fatalf("maintain --check exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "review-needed" || m["pending"] != float64(base+3) {
		t.Fatalf("초과 status=%v pending=%v, want review-needed/%d; out=%s",
			m["status"], m["pending"], base+3, out)
	}
}

func TestDoneWarnsConsecutiveUnrecordedSessions(t *testing.T) {
	pool := mustDB(t)
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM sessions WHERE agent = $1`, "e2e-done-agent"); err != nil {
		t.Fatalf("sessions 정리 실패: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM sessions WHERE agent = $1`, "e2e-done-agent")
	})
	setMeta(t, pool, "stale_sessions", "3")
	t.Cleanup(func() { delMeta(t, pool, "stale_sessions") })

	setMeta(t, pool, "recall_warn_writes", "1000000")
	t.Cleanup(func() { delMeta(t, pool, "recall_warn_writes") })

	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	latestSessionID := func() string {
		t.Helper()
		var id int
		if err := pool.QueryRow(context.Background(),
			`SELECT max(id) FROM sessions WHERE agent = $1`, "e2e-done-agent").Scan(&id); err != nil {
			t.Fatalf("session id 조회 실패: %v", err)
		}
		return strconv.Itoa(id)
	}

	out, code := runCLI(t, []string{"write", "session"},
		`{"agent":"e2e-done-agent","summary":"미기록 1"}`+"\n"+
			`{"agent":"e2e-done-agent","summary":"미기록 2"}`+"\n")
	if code != 0 {
		t.Fatalf("session write exit=%d, want 0; out=%s", code, out)
	}
	out, code = runCLI(t, []string{"done", "--session", latestSessionID()}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0(종료 불가); out=%s", code, out)
	}
	if m := decode(t, out); m["status"] != "ok" {
		t.Fatalf("연속 2 status=%v, want ok; out=%s", m["status"], out)
	}

	out, code = runCLI(t, []string{"write", "session"},
		`{"agent":"e2e-done-agent","summary":"미기록 3"}`+"\n")
	if code != 0 {
		t.Fatalf("session write exit=%d, want 0; out=%s", code, out)
	}
	out, code = runCLI(t, []string{"done", "--session", latestSessionID()}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0(경고여도 종료 가능); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "stale" || m["runs"] != float64(3) {
		t.Fatalf("연속 3 출력=%v, want stale/3", m)
	}
	if w, _ := m["warning"].(string); !strings.Contains(w, "3 consecutive sessions unrecorded") {
		t.Fatalf("warning=%q, want 3 consecutive sessions unrecorded", w)
	}
}

func TestDoneTriggersAutoBackupAfter24h(t *testing.T) {
	pool := mustDB(t)
	stale := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	setMeta(t, pool, "last_backup", stale)
	t.Cleanup(func() { delMeta(t, pool, "last_backup") })

	dir := t.TempDir()
	t.Setenv("FLINT_BACKUP_DIR", dir)
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	out, code := runCLI(t, []string{"done", "--session", "e2e-backup-probe"}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0; out=%s", code, out)
	}
	decode(t, out)

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("자동 백업 파일 미생성(entries=%d, err=%v)", len(entries), err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			t.Fatalf("백업 파일 %s 비어 있음(size=0)", e.Name())
		}
	}

	var last string
	if err := pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'last_backup'`).Scan(&last); err != nil {
		t.Fatalf("last_backup 조회 실패: %v", err)
	}
	if last == stale {
		t.Fatalf("last_backup 미갱신: %q", last)
	}
}

func mcpHandle(method string, params map[string]any) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}, nil
	case "tools/list":
		return map[string]any{"tools": mcp.ToolsManifest()}, nil
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		out, code := mcp.DispatchTool(name, args)
		return map[string]any{
			"content":  []map[string]any{{"type": "text", "text": out}},
			"exitCode": code,
		}, nil
	default:
		return nil, mcp.ErrUnknownMethod
	}
}

func TestMCPRoundtripRealDB(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mcp-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mcp-") })

	writeKnowledge(t, `{"topic":"e2e-mcp-k","summary":"MCP 왕복 프로브","confidence":0.9}`)

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"e2e-mcp-k","limit":5}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := mcp.Serve(strings.NewReader(reqs), &out, mcpHandle); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" {
			resps = append(resps, decode(t, l))
		}
	}
	if len(resps) != 3 {
		t.Fatalf("응답 %d행, want 3(요청마다 1행); out=%s", len(resps), out.String())
	}
	for i, m := range resps {
		if m["id"] != float64(i+1) {
			t.Fatalf("응답[%d] id=%v, want %d(id 대응); out=%s", i, m["id"], i+1, out.String())
		}
		if _, bad := m["error"]; bad {
			t.Fatalf("응답[%d] error=%v; out=%s", i, m["error"], out.String())
		}
	}

	tools, _ := resps[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 6 {
		t.Fatalf("도구 %d건, want 6(flint_work 포함); out=%s", len(tools), out.String())
	}
	got := map[string]bool{}
	for _, tl := range tools {
		if td, ok := tl.(map[string]any); ok {
			got[td["name"].(string)] = true
		}
	}
	for _, want := range []string{"flint_read", "flint_write", "flint_hot", "flint_work"} {
		if !got[want] {
			t.Fatalf("tools/list에 %q 없음; out=%s", want, out.String())
		}
	}

	result, _ := resps[2]["result"].(map[string]any)
	if ec, _ := result["exitCode"].(float64); ec != 0 {
		t.Fatalf("flint_read exitCode=%v, want 0; out=%s", result["exitCode"], out.String())
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("flint_read content 비어 있음; out=%s", out.String())
	}
	if text, _ := content[0].(map[string]any)["text"].(string); !strings.Contains(text, "e2e-mcp-k") {
		t.Fatalf("flint_read 결과에 씨딩 토픽 없음: %q", text)
	}
}

func TestHotMetricsShowInStatus(t *testing.T) {
	mustDB(t)

	if _, code := runCLI(t, []string{"hot"}, ""); code != 0 {
		t.Fatalf("hot exit=%d, want 0", code)
	}
	out, code := runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if calls, _ := m["hot_calls"].(float64); calls < 1 {
		t.Fatalf("hot_calls=%v, want >=1; out=%s", m["hot_calls"], out)
	}
	if _, ok := m["hot_p95"]; !ok {
		t.Fatalf("status에 hot_p95 미노출; out=%s", out)
	}
}

func TestVersionBinaryJSON(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}

	vcmd := exec.Command(filepath.Join(root, "bin", "flint"), "version")
	vcmd.Env = append(os.Environ(), "FLINT_DSN=postgresql://flint:wrong@127.0.0.1:1/flint")
	b, err := vcmd.Output()
	if err != nil {
		t.Fatalf("version exit 오류(store-free여야 함): %v; out=%s", err, b)
	}
	m := decode(t, string(b))
	if v, _ := m["version"].(string); v == "" {
		t.Fatalf("version 필드 비어 있음: %s", b)
	}
	if c, _ := m["commit"].(string); c == "" {
		t.Fatalf("commit 필드 비어 있음: %s", b)
	}
}

func TestSchemaV2PromotedAfterDone(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-v3-")
	t.Cleanup(func() {
		cleanupTopics(t, pool, "e2e-v3-")

		setMeta(t, pool, "schema_version", "1")
	})

	t.Setenv("FLINT_BACKUP_DIR", t.TempDir())

	setMeta(t, pool, "schema_version", "1")
	writeKnowledge(t, `{"topic":"e2e-v3-k","summary":"v2 승격 프로브","confidence":0.9}`)

	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	out, code := runCLI(t, []string{"done", "--session", "e2e-v3-probe"}, "")
	if code != 0 {
		t.Fatalf("done exit=%d, want 0; out=%s", code, out)
	}

	var version string
	if err := pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema_version 조회 실패: %v", err)
	}

	want := strconv.Itoa(db.LatestSchemaVersion)
	if version != want {
		t.Fatalf("done 후 schema_version=%q, want %s(연쇄 승격)", version, want)
	}

	lines := readLines(t, "e2e-v3-k")
	if len(lines) == 0 || !strings.Contains(lines[0], "e2e-v3-k") {
		t.Fatalf("승격 후 read 결과 이상: %v", lines)
	}

	out, code = runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	if m := decode(t, out); m["schema_version"] != strconv.Itoa(db.LatestSchemaVersion) {
		t.Fatalf("status schema_version=%v, want %d; out=%s", m["schema_version"], db.LatestSchemaVersion, out)
	}
}

func TestWriteWarnsUnknownTag(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-vocab-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-vocab-") })

	out, code := runCLI(t, []string{"write", "knowledge"},
		`{"topic":"e2e-vocab-k","summary":"어휘 경고 프로브","confidence":0.9,"tags":["playbook","bogus"],"origin":"demo/e2e","status":"verified"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(경고는 저장 거부 아님); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "committed" {
		t.Fatalf("status=%v, want committed; out=%s", m["status"], out)
	}
	found := false
	for _, w := range toStrings(m["warnings"]) {
		if strings.Contains(w, "unknown tag: bogus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings에 %q 없음: %v", "unknown tag: bogus", m["warnings"])
	}
}

func toStrings(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestPatternRepeatSuccessCandidate(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-pat-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-pat-") })

	writeKnowledge(t, `{"topic":"e2e-pat-k","summary":"성공 1","confidence":0.9,"tags":["process"],"origin":"demo/e2e","status":"verified"}`)
	writeKnowledge(t, `{"topic":"e2e-pat-k","summary":"성공 2","confidence":0.9,"tags":["process"],"origin":"demo/e2e","status":"verified"}`)

	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	pats, err := repo.PatternCandidates(context.Background())
	if err != nil {
		t.Fatalf("PatternCandidates: %v", err)
	}
	for _, p := range pats {
		if p.Topic != "e2e-pat-k" {
			continue
		}
		if p.Kind != "repeat-success" {
			t.Fatalf("kind=%q, want repeat-success", p.Kind)
		}
		if len(p.Evidence) != 2 {
			t.Fatalf("증거 %d건, want 2: %v", len(p.Evidence), p.Evidence)
		}
		return
	}
	t.Fatalf("후보에 %q 없음: %+v", "e2e-pat-k", pats)
}

func TestPlaybookUseStatsRate(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-usage-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-usage-") })

	m := writeKnowledge(t,
		`{"topic":"e2e-usage-k","summary":"사용 로그 프로브","confidence":0.9,"tags":["playbook"],"origin":"demo/e2e","status":"verified"}`)
	id := int(m["id"].(float64))
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1 OR key = $2`,
			fmtPBKey(id, "uses"), fmtPBKey(id, "ok"))
	})

	repo, err := db.NewRepo(context.Background(), dsn())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	for _, ok := range []bool{true, true, false} {
		if err := repo.LogPlaybookUse(context.Background(), id, ok); err != nil {
			t.Fatalf("LogPlaybookUse(%v): %v", ok, err)
		}
	}
	stats, err := repo.PlaybookStats(context.Background())
	if err != nil {
		t.Fatalf("PlaybookStats: %v", err)
	}
	for _, s := range stats {
		if s.ID != id {
			continue
		}
		if s.Uses != 3 || s.OK != 2 {
			t.Fatalf("uses/ok = %d/%d, want 3/2", s.Uses, s.OK)
		}
		if s.Rate != 0.667 {
			t.Fatalf("rate = %v, want 0.667", s.Rate)
		}
		return
	}
	t.Fatalf("stats에 id=%d 없음: %+v", id, stats)
}

func fmtPBKey(id int, suffix string) string {
	return "pb_" + strconv.Itoa(id) + "_" + suffix
}

func TestHotCallsAccumulateAcrossProcesses(t *testing.T) {
	pool := mustDB(t)
	delMeta(t, pool, "hot_stats")
	t.Cleanup(func() { delMeta(t, pool, "hot_stats") })

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	bin := filepath.Join(root, "bin", "flint")
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}
	for i := 0; i < 2; i++ {
		hot := exec.Command(bin, "hot")
		hot.Env = os.Environ()
		if b, err := hot.CombinedOutput(); err != nil {
			t.Fatalf("hot %d회차 실패: %v; out=%s", i+1, err, b)
		}
	}
	out, code := runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if calls, _ := m["hot_calls"].(float64); calls != 2 {
		t.Fatalf("hot_calls=%v, want 2(프로세스 분리 누적); out=%s", m["hot_calls"], out)
	}
}

func TestSetupDrySteps(t *testing.T) {
	out, code := runCLI(t, []string{"setup", "--dry"}, "")
	if code != 0 {
		t.Fatalf("setup --dry exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "dry" {
		t.Fatalf("status=%v, want dry; out=%s", m["status"], out)
	}
	if steps := toStrings(m["steps"]); len(steps) != 4 {
		t.Fatalf("단계 %d건, want 4; out=%s", len(steps), out)
	}
}

func rebuildBin(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("루트 경로: %v", err)
	}
	build := exec.Command("make", "build")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build 실패: %v; out=%s", err, b)
	}
	return filepath.Join(root, "bin", "flint")
}

func TestMCPRealProcessRoundtrip(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-mcpv5-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-mcpv5-") })
	writeKnowledge(t, `{"topic":"e2e-mcpv5-k","summary":"실 프로세스 MCP 프로브","confidence":0.9}`)

	bin := rebuildBin(t)
	cmd := exec.Command(bin, "mcp")
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin 파이프: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("mcp 프로세스 기동: %v", err)
	}
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"e2e-mcpv5-k","limit":5}}}`,
	}, "\n") + "\n"
	if _, err := io.WriteString(stdin, reqs); err != nil {
		t.Fatalf("stdin 기록: %v", err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("mcp exit 오류(EOF 정상 0이어야 함): %v; out=%s", err, out.String())
	}

	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" {
			resps = append(resps, decode(t, l))
		}
	}
	if len(resps) != 2 {
		t.Fatalf("응답 %d행, want 2; out=%s", len(resps), out.String())
	}
	for i, m := range resps {
		if m["id"] != float64(i+1) {
			t.Fatalf("응답[%d] id=%v, want %d(순서 대응); out=%s", i, m["id"], i+1, out.String())
		}
		if _, bad := m["error"]; bad {
			t.Fatalf("응답[%d] error=%v; out=%s", i, m["error"], out.String())
		}
	}
	initRes, _ := resps[0]["result"].(map[string]any)
	if initRes["protocolVersion"] == nil {
		t.Fatalf("initialize result에 protocolVersion 없음; out=%s", out.String())
	}
	callRes, _ := resps[1]["result"].(map[string]any)
	if callRes["isError"] != false {
		t.Fatalf("flint_read isError=%v, want false; out=%s", callRes["isError"], out.String())
	}
	content, _ := callRes["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("flint_read content 비어 있음; out=%s", out.String())
	}
	if text, _ := content[0].(map[string]any)["text"].(string); !strings.Contains(text, "e2e-mcpv5-k") {
		t.Fatalf("flint_read 결과에 씨딩 토픽 없음: %q", text)
	}
}

func TestAuditSessionFilter(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-audit-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-audit-") })

	seed := func(session, topic string) {
		t.Helper()
		out, code := runCLI(t, []string{"write", "knowledge", "--session", session},
			`{"topic":"`+topic+`","summary":"감사 필터 프로브","confidence":0.9}`)
		if code != 0 {
			t.Fatalf("write(%s) exit=%d, want 0; out=%s", session, code, out)
		}
	}
	seed("e2e-audit-s1", "e2e-audit-k1")
	seed("e2e-audit-s2", "e2e-audit-k2")

	out, code := runCLI(t, []string{"audit", "--session", "e2e-audit-s1"}, "")
	if code != 0 {
		t.Fatalf("audit exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	log, _ := m["log"].([]any)
	if len(log) == 0 {
		t.Fatalf("log 0행, want >=1; out=%s", out)
	}
	for _, e := range log {
		msg, _ := e.(map[string]any)["message"].(string)
		if !strings.Contains(msg, "[session:e2e-audit-s1]") {
			t.Fatalf("세션 필터 누출: %q", msg)
		}
		if strings.Contains(msg, "e2e-audit-s2") {
			t.Fatalf("다른 세션 커밋 혼입: %q", msg)
		}
	}
}

func TestRotateE2E(t *testing.T) {
	pool := mustDB(t)
	setMeta(t, pool, "backup_keep", "2")
	t.Cleanup(func() { delMeta(t, pool, "backup_keep") })
	dir := t.TempDir()
	t.Setenv("FLINT_BACKUP_DIR", dir)
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	flintDumps := func() []string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(dir, "flint-*.sql"))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		sort.Strings(matches)
		return matches
	}

	stale := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	autoBackup := func(i int) string {
		t.Helper()
		setMeta(t, pool, "last_backup", stale)
		before := flintDumps()
		out, code := runCLI(t, []string{"done", "--session", "e2e-rotate-" + strconv.Itoa(i)}, "")
		if code != 0 {
			t.Fatalf("done %d exit=%d, want 0; out=%s", i, code, out)
		}
		var created []string
		for _, p := range flintDumps() {
			if !slices.Contains(before, p) {
				created = append(created, p)
			}
		}
		if len(created) != 1 {
			t.Fatalf("자동 백업 파일 %d개 생성, want 1; dir=%s; out=%s", len(created), dir, out)
		}
		return created[0]
	}

	nextSecond := func() {
		now := time.Now()
		time.Sleep(time.Until(now.Truncate(time.Second).Add(time.Second)))
	}

	base := time.Now().Add(-time.Hour)
	stamp := func(path string, i int) {
		t.Helper()
		when := base.Add(time.Duration(i) * 10 * time.Minute)
		for _, suffix := range []string{"", ".sha256"} {
			_ = os.Chtimes(path+suffix, when, when)
		}
	}

	nextSecond()
	oldest := autoBackup(1)
	stamp(oldest, 1)
	nextSecond()
	mid := autoBackup(2)
	stamp(mid, 2)

	mine := filepath.Join(dir, "mine.sql")
	out, code := runCLI(t, []string{"backup", "--out", mine}, "")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("backup --out exit=%d; out=%s", code, out)
	}

	nextSecond()
	autoBackup(3)

	remain := flintDumps()
	if len(remain) != 2 {
		t.Fatalf("잔존 flint 덤프=%v, want 2쌍(mid·newest)", remain)
	}
	if remain[0] != mid || remain[1] == oldest {
		t.Fatalf("잔존=%v, want mid+newest(oldest=%s 삭제)", remain, oldest)
	}
	for _, p := range append(remain, mine) {
		for _, suffix := range []string{"", ".sha256"} {
			if _, err := os.Stat(p + suffix); err != nil {
				t.Fatalf("잔존 파일+사이드카 확인 실패 %s: %v", p+suffix, err)
			}
		}
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("가장 오래된 덤프 미삭제: %s (err=%v)", oldest, err)
	}
	if _, err := os.Stat(oldest + ".sha256"); !os.IsNotExist(err) {
		t.Fatalf("가장 오래된 사이드카 미삭제: %s.sha256 (err=%v)", oldest, err)
	}
}

func TestDrillE2EAndStats(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-drill-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-drill-") })
	delMeta(t, pool, "read_stats")
	t.Cleanup(func() { delMeta(t, pool, "read_stats") })

	writeKnowledge(t, `{"topic":"e2e-drill-k","summary":"드릴 프로브","confidence":0.9}`)
	dir := t.TempDir()
	dump := filepath.Join(dir, "drill.sql")
	if out, code := runCLI(t, []string{"backup", "--out", dump}, ""); code != 0 {
		t.Fatalf("backup exit=%d, want 0; out=%s", code, out)
	}

	out, code := runCLI(t, []string{"restore", "--drill", "--file", dump}, "")
	if code != 0 {
		t.Fatalf("restore --drill exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "drill" || m["ok"] != true {
		t.Fatalf("drill 출력=%v, want status=drill/ok=true", m)
	}
	if m["expected"] != m["restored"] || m["expected"] == float64(0) {
		t.Fatalf("expected=%v restored=%v, want 동일且 >=1", m["expected"], m["restored"])
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'flint_drill'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("flint_drill 잔존=%d(항상 DROP이어야 함), err=%v", n, err)
	}

	readLines(t, "e2e-drill-k")
	out, code = runCLI(t, []string{"status"}, "")
	if code != 0 {
		t.Fatalf("status exit=%d, want 0; out=%s", code, out)
	}
	sm := decode(t, out)
	if calls, _ := sm["read_calls"].(float64); calls < 1 {
		t.Fatalf("read_calls=%v, want >=1; out=%s", sm["read_calls"], out)
	}
	if _, ok := sm["read_zero_pct"]; !ok {
		t.Fatalf("status에 read_zero_pct 미노출; out=%s", out)
	}
}

func TestReadRankingOrder(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-rank-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-rank-") })

	writeKnowledge(t, `{"topic":"e2e-rank-k","summary":"정합 우선 프로브","confidence":0.5}`)
	writeKnowledge(t, `{"topic":"e2e-rank-a","summary":"e2e-rank-k 언급 높은 신뢰","confidence":0.9}`)
	writeKnowledge(t, `{"topic":"e2e-rank-b","summary":"e2e-rank-k 재인용 낮은 신뢰","confidence":0.8}`)

	out, code := runCLI(t, []string{"read", "e2e-rank-k", "--json", "--fields", "topic,confidence"}, "")
	if code != 0 {
		t.Fatalf("read exit=%d, want 0; out=%s", code, out)
	}
	var got []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			got = append(got, decode(t, l))
		}
	}
	if len(got) != 3 {
		t.Fatalf("결과 %d건, want 3; out=%s", len(got), out)
	}
	wantTopics := []string{"e2e-rank-k", "e2e-rank-a", "e2e-rank-b"}
	for i, want := range wantTopics {
		if got[i]["topic"] != want {
			t.Fatalf("순위[%d] topic=%v, want %v(topic 정합 > confidence); 전체=%s",
				i, got[i]["topic"], want, out)
		}
	}
}

func TestReportEffectFlow(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-effect-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-effect-") })
	ctx := context.Background()

	const label = "e2e-s1"
	for _, stmt := range []string{
		`DELETE FROM read_events WHERE session = $1`,
		`DELETE FROM session_starts WHERE session = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, label); err != nil {
			t.Fatalf("계측 사전 정리: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM read_events WHERE session = $1`,
			`DELETE FROM session_starts WHERE session = $1`,
		} {
			_, _ = pool.Exec(context.Background(), stmt, label)
		}
	})
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	writeKnowledge(t, `{"topic":"e2e-effect-k","summary":"효과 계측 프로브","confidence":0.9}`)

	if out, code := runCLI(t, []string{"session-start", "--session", label}, ""); code != 0 {
		t.Fatalf("session-start exit=%d, want 0; out=%s", code, out)
	}
	var starts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM session_starts WHERE session = $1`, label).Scan(&starts); err != nil {
		t.Fatalf("session_starts 조회: %v", err)
	}
	if starts != 1 {
		t.Fatalf("세션 경계 %d건, want 1", starts)
	}

	out, code := runCLI(t, []string{"read", "e2e-effect-k", "--session", label}, "")
	if code != 0 {
		t.Fatalf("read exit=%d, want 0; out=%s", code, out)
	}

	writeKnowledge(t, `{"topic":"e2e-effect-k","summary":"연쇄 검증 기록","confidence":0.9}`)

	var kid int
	if err := pool.QueryRow(ctx,
		`SELECT id FROM knowledge WHERE topic = 'e2e-effect-k' ORDER BY id LIMIT 1`).Scan(&kid); err != nil {
		t.Fatalf("프로브 지식 id 조회: %v", err)
	}
	var idsCSV string
	if err := pool.QueryRow(ctx,
		`SELECT ids FROM read_events WHERE session = $1`, label).Scan(&idsCSV); err != nil {
		t.Fatalf("read_events ids 조회: %v", err)
	}
	if !strings.Contains(idsCSV, fmt.Sprint(kid)) {
		t.Fatalf("ids=%q가 프로브 id %d 미포함(반환 id 파이프라인)", idsCSV, kid)
	}

	out, code = runCLI(t, []string{"report", "--days", "1"}, "")
	if code != 0 {
		t.Fatalf("report exit=%d, want 0; out=%s", code, out)
	}
	rep := decode(t, strings.TrimSpace(out))
	effect, ok := rep["effect"].(map[string]any)
	if !ok {
		t.Fatalf("report에 effect 섹션 없음: %s", out)
	}
	for _, key := range []string{
		"hit_pct", "near_pct", "fallback_pct", "dup_prevented", "reuse_pct", "dead_pct",
		"read_events_n", "read_write_pairs", "pair_pct", "recall_led_pct", "chain_pct",
	} {
		if _, ok := effect[key]; !ok {
			t.Errorf("effect.%s 누락: %v", key, effect)
		}
	}

	if n, _ := effect["read_events_n"].(float64); n < 1 {
		t.Errorf("read_events_n = %v, want ≥ 1", effect["read_events_n"])
	}
	if p, _ := effect["read_write_pairs"].(float64); p < 1 {
		t.Errorf("read_write_pairs = %v, want ≥ 1(회상 뒤 기록 동반)", effect["read_write_pairs"])
	}

	if led, _ := effect["recall_led_pct"].(float64); led != 100 {
		t.Errorf("recall_led_pct = %v, want 100(회상 선도 세션)", effect["recall_led_pct"])
	}
}

func TestBackupEncryptDrillE2E(t *testing.T) {
	pool := mustDB(t)
	cleanupTopics(t, pool, "e2e-encdrill-")
	t.Cleanup(func() { cleanupTopics(t, pool, "e2e-encdrill-") })
	writeKnowledge(t, `{"topic":"e2e-encdrill-k","summary":"암호화 드릴 프로브","confidence":0.9}`)

	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	dump := filepath.Join(dir, "drill.sql.age")
	out, code := runCLI(t, []string{"backup", "--out", dump, "--encrypt"}, "")
	if code != 0 {
		t.Fatalf("backup --encrypt exit=%d; out=%s", code, out)
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("age-encryption.org/")) {
		t.Fatalf("age 헤더 없음: %q", b[:20])
	}
	out, code = runCLI(t, []string{"restore", "--drill", "--file", dump}, "")
	if code != 0 {
		t.Fatalf("restore --drill(암호문) exit=%d; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "drill" || m["ok"] != true {
		t.Fatalf("암호문 drill 출력=%v, want ok=true", m)
	}
	if m["expected"] != m["restored"] || m["expected"] == float64(0) {
		t.Fatalf("expected=%v restored=%v", m["expected"], m["restored"])
	}

	masked := filepath.Join(dir, "m.sql")
	out, code = runCLI(t, []string{"backup", "--out", masked, "--mask"}, "")
	if code != 0 {
		t.Fatalf("backup --mask exit=%d; out=%s", code, out)
	}
	if _, serr := os.Stat(masked + ".sha256"); serr == nil {
		t.Fatal("마스킹 덤프에 사이드카 부여 — 비복원 계약 위반")
	}
	out, code = runCLI(t, []string{"restore", "--drill", "--file", masked}, "")
	if code == 1 && strings.Contains(out, "sidecar") {
		return
	}
	if code != 1 {
		t.Fatalf("마스킹 drill = exit %d %s, want 1", code, out)
	}
}

func dbPassword(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(dsn())
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			return pw
		}
	}
	return "flint-dev"
}
