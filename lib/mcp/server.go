package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var ErrUnknownMethod = errors.New("method not found")

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

const serveMaxLine = 4 * 1024 * 1024

func Serve(r io.Reader, w io.Writer, handle func(method string, params map[string]any) (any, error)) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	br := bufio.NewReaderSize(r, 64*1024)
	buf := new(bytes.Buffer)
	for {
		tooLong := false
		eof := false
		buf.Reset()
		for {
			chunk, err := br.ReadSlice('\n')
			if !tooLong {
				if buf.Len()+len(chunk) > serveMaxLine {
					tooLong = true
					buf.Reset()
				} else {
					buf.Write(chunk)
				}
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err == io.EOF {
				eof = true
			} else if err != nil {
				return err
			}
			break
		}
		if tooLong {
			if werr := writeTooLongResponse(bw); werr != nil {
				return werr
			}
			if eof {
				return nil
			}
			continue
		}
		line := bytes.TrimSpace(buf.Bytes())
		if len(line) > 0 {
			var req rpcRequest
			if err := json.Unmarshal(line, &req); err == nil && req.ID != nil && req.Method != "" {
				if err := writeResponse(bw, req, handle); err != nil {
					return err
				}
				if err := bw.Flush(); err != nil {
					return err
				}
			}
		}
		if eof {
			return nil
		}
	}
}

func writeTooLongResponse(bw *bufio.Writer) error {
	resp := response{JSONRPC: "2.0", Error: &rpcError{
		Code:    -32600,
		Message: "invalid request: line exceeds 4MB limit",
	}}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	if _, werr := bw.Write(append(b, '\n')); werr != nil {
		return werr
	}
	return bw.Flush()
}

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
		code := -32603
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
