// Package mcp implements a minimal newline-delimited JSON-RPC 2.0 server
// over stdio for the flint MCP interface.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrUnknownMethod signals that the handler does not know the requested
// method; Serve maps it to JSON-RPC error -32601.
var ErrUnknownMethod = errors.New("method not found")

// rpcRequest is one incoming JSON-RPC message. id == nil means notification.
type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// Serve reads newline-delimited JSON-RPC 2.0 messages from r, dispatches
// each to handle, and writes exactly one response line per request to w.
// Notifications (no id) get no reply. Malformed lines are skipped so the
// server keeps serving subsequent messages. Serve returns when r is exhausted.
func Serve(r io.Reader, w io.Writer, handle func(method string, params map[string]any) (any, error)) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // write payloads can exceed the 64KB default
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // malformed line: skip, stay alive
		}
		if req.ID == nil || req.Method == "" {
			continue // notification or unidentifiable: no reply
		}
		if err := writeResponse(bw, req, handle); err != nil {
			return err
		}
	}
	return sc.Err()
}
// response is one outgoing JSON-RPC line; exactly one of Result/Error is set.
type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeResponse(bw *bufio.Writer, req rpcRequest, handle func(method string, params map[string]any) (any, error)) error {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	result, err := handle(req.Method, req.Params)
	if err != nil {
		code := -32603 // internal error
		if errors.Is(err, ErrUnknownMethod) {
			code = -32601
		}
		resp.Error = &rpcError{Code: code, Message: err.Error()}
	} else {
		resp.Result = result
	}
	enc, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := bw.Write(append(enc, '\n')); err != nil {
		return err
	}
	return nil
}
