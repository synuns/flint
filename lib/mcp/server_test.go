package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPingRoundtrip(t *testing.T) {
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}` + "\n")
	err := Serve(&in, &out, func(method string, params map[string]any) (any, error) {
		if method != "ping" {
			t.Fatalf("method = %q, want ping", method)
		}
		return "pong", nil
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("output lines = %d, want 1: %q", len(lines), out.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v — line %q", err, lines[0])
	}
	if got["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", got["jsonrpc"])
	}
	if id, ok := got["id"].(float64); !ok || id != 1 {
		t.Errorf("id = %v, want 1", got["id"])
	}
	if got["result"] != "pong" {
		t.Errorf("result = %v, want pong", got["result"])
	}
}

func TestNotificationNoReply(t *testing.T) {
	var in, out bytes.Buffer
	called := map[string]int{}
	in.WriteString(`{"jsonrpc":"2.0","method":"notify","params":{"k":"v"}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"k":"v"}}` + "\n")
	err := Serve(&in, &out, func(method string, params map[string]any) (any, error) {
		called[method]++
		if params["k"] != "v" {
			t.Errorf("params = %v, want k=v forwarded", params)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("output lines = %d, want 1 (notification must not reply): %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"id":2`) {
		t.Errorf("only reply should be for id 2, got %q", lines[0])
	}
	if called["notify"] != 0 {
		t.Errorf("handler called %d times for notification, want 0", called["notify"])
	}
	if called["ping"] != 1 {
		t.Errorf("handler called %d times for ping, want 1", called["ping"])
	}
}

func TestUnknownMethod(t *testing.T) {
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":7,"method":"nope","params":{}}` + "\n")
	in.WriteString("this is not json\n")
	in.WriteString(`{"jsonrpc":"2.0","id":8,"method":"ping","params":{}}` + "\n")
	err := Serve(&in, &out, func(method string, params map[string]any) (any, error) {
		if method == "nope" {
			return nil, ErrUnknownMethod
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Serve: %v (malformed line must not kill server)", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("output lines = %d, want 2 (malformed skipped): %q", len(lines), out.String())
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rpcErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing: %q", lines[0])
	}
	if code, ok := rpcErr["code"].(float64); !ok || int(code) != -32601 {
		t.Errorf("error.code = %v, want -32601", rpcErr["code"])
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if second["result"] != "ok" {
		t.Errorf("server died after malformed line, result = %v", second["result"])
	}
}

// TestServeSkipTooLongLine — 상한 초과 줄 이후 요청은
// 여전히 처리된다(서버 생존).
func TestServeSkipTooLongLine(t *testing.T) {
	huge := strings.Repeat("x", 5*1024*1024) + "\n"
	normal := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	err := Serve(io.MultiReader(strings.NewReader(huge), strings.NewReader(normal)), &out, func(m string, p map[string]any) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatalf("Serve err=%v — 초과 줄로 서버 사망(생존 계약 위반)", err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("초과 줄 이후 정상 요청 미처리: %q", out.String())
	}
}

// TestServeFlushesEachResponseImmediately — 각 응답은 write
// 직후 즉시 flush된다 — 입력 EOF 전까지 bufio 버퍼에 머무르면 요청-응답
// 왕복 클라이언트가 데드락한다. io.Pipe로 입력을 열어 둔 채 응답 도달을
// 단언(EOF 폭주 시 deferred flush로도 늦게 도달하므로 타임아웃이 판정).
func TestServeFlushesEachResponseImmediately(t *testing.T) {
	pr, pw := io.Pipe() // 입력 — 닫지 않고 열어 둔다(EOF 없음)
	cr, cw := io.Pipe() // 출력 — Serve의 w
	defer pr.Close()

	go func() {
		_ = Serve(pr, cw, func(method string, params map[string]any) (any, error) {
			return "ok", nil
		})
		cw.Close()
	}()
	go func() {
		_, _ = pw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}` + "\n"))
	}()

	type lineResult struct {
		line []byte
		err  error
	}
	done := make(chan lineResult, 1)
	go func() {
		var one [512]byte
		n, err := cr.Read(one[:]) // 출력 파이프에서 개행 1줄이 오려면 flush돼야 한다
		done <- lineResult{one[:n], err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("응답 읽기: %v", res.err)
		}
		if !strings.Contains(string(res.line), `"id":1`) {
			t.Fatalf("응답 내용: %q", res.line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("EOF 전 응답 미도달 — 응답 후 즉시 flush 없음(버퍼 적체 데드락)")
	}
	pw.Close()
}

// TestServeTooLongLineGetsErrorResponse — 초과 줄을
// 무응답으로 버리면 응답을 기다리는 클라이언트가 타임아웃한다. 초과 줄에는
// -32600 응답 1줄을 돌려주고(id는 복원 불가라 null), 이후 요청은 계속
// 처리된다.
func TestServeTooLongLineGetsErrorResponse(t *testing.T) {
	huge := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"arguments":{"payload":"` +
		strings.Repeat("x", 5*1024*1024) + `"}}` + "\n"
	normal := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	err := Serve(io.MultiReader(strings.NewReader(huge), strings.NewReader(normal)), &out, func(m string, p map[string]any) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	if err != nil {
		t.Fatalf("Serve err=%v — 초과 줄로 서버 사망(생존 계약 위반)", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("output lines = %d, want 2(초과 줄 -32600 응답 1 + 정상 응답 1): %q", len(lines), out.String())
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("unmarshal: %v — line %q", err, lines[0])
	}
	if resp["id"] != nil {
		t.Errorf("초과 줄 응답 id = %v, want null(id 복원 불가)", resp["id"])
	}
	rpcErr, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error 필드 없음: %q", lines[0])
	}
	if code, ok := rpcErr["code"].(float64); !ok || int(code) != -32600 {
		t.Errorf("error.code = %v, want -32600", rpcErr["code"])
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if id, _ := second["id"].(float64); id != 1 {
		t.Errorf("초과 줄 이후 정상 요청 미처리: %q", lines[1])
	}
}
