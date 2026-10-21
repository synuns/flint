package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
