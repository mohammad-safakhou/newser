package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ==========================================================
// MCP stdio JSON-RPC client
// ==========================================================

type MCPClient interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
	Close() error
}

type MCPTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]any    `json:"input_schema"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type stdioMCP struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	mu  sync.Mutex
	seq int64
}

func StartStdioMCP(ctx context.Context, command string, args ...string) (MCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &stdioMCP{cmd: cmd, in: stdin, out: bufio.NewReader(stdout)}, nil
}

type rpcReq struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *stdioMCP) send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	req := rpcReq{JSONRPC: "2.0", ID: c.seq, Method: method, Params: params}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if _, err := c.in.Write(b); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(DefaultToolTimeout)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("mcp: timeout for %s", method)
		}
		var buf bytes.Buffer
		for {
			frag, err := c.out.ReadBytes('\n')
			buf.Write(frag)
			if buf.Len() > MaxJSONFrameBytes {
				return nil, fmt.Errorf("mcp: frame too large")
			}
			if err == nil {
				break
			}
			if err == io.EOF {
				return nil, io.EOF
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				return nil, err
			}
		}
		line := bytes.TrimSpace(buf.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var resp rpcResp
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *stdioMCP) ListTools(ctx context.Context) ([]MCPTool, error) {
	res, err := c.send(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	raw, ok := res["tools"].([]any)
	if !ok {
		return nil, errors.New("invalid tools/list")
	}
	out := make([]MCPTool, 0, len(raw))
	for _, v := range raw {
		b, _ := json.Marshal(v)
		var t MCPTool
		_ = json.Unmarshal(b, &t)
		out = append(out, t)
	}
	return out, nil
}
func (c *stdioMCP) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	return c.send(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
}
func (c *stdioMCP) Close() error { _ = c.in.Close(); return c.cmd.Wait() }
