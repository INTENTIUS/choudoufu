// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion is the MCP revision this package speaks, as a client to
// chant's and behold's servers and as a server to an agent.
const ProtocolVersion = "2025-06-18"

// RemoteTool is one tool a child MCP server lists.
type RemoteTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// ToolResult is an MCP tools/call result, carried as it came.
type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is one entry of a tool result's content. Fields other than
// type and text (an image's data and mimeType) are kept in Raw.
type ContentItem struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Raw  json.RawMessage `json:"-"`
}

func (c ContentItem) MarshalJSON() ([]byte, error) {
	if len(c.Raw) > 0 {
		return c.Raw, nil
	}
	type plain ContentItem
	return json.Marshal(plain(c))
}

func (c *ContentItem) UnmarshalJSON(b []byte) error {
	type plain ContentItem
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = ContentItem(p)
	c.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// Text joins the text items of a result.
func (r ToolResult) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// MCPClient is a client of one child MCP server over its stdio:
// newline-delimited JSON-RPC 2.0, one request at a time.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr lockedBuffer

	mu     sync.Mutex
	nextID int
	Server struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
}

// StartMCP starts bin with args in dir and runs the initialize handshake.
func StartMCP(ctx context.Context, bin string, args []string, dir string, env []string) (*MCPClient, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &MCPClient{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20)}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := c.call(ictx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "choudoufu", "version": "investigate"},
	}, &init); err != nil {
		c.Close()
		return nil, fmt.Errorf("%s %s did not complete the MCP handshake: %w", bin, strings.Join(args, " "), err)
	}
	c.Server.Name, c.Server.Version = init.ServerInfo.Name, init.ServerInfo.Version
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// ListTools lists every tool the server has, following nextCursor.
func (c *MCPClient) ListTools(ctx context.Context) ([]RemoteTool, error) {
	var all []RemoteTool
	cursor := ""
	for i := 0; i < 100; i++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []RemoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
	return nil, errors.New("tools/list never ended its pages")
}

// CallTool calls one tool and returns its result as the server gave it.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	var res ToolResult
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Close ends the child: its stdin is closed, which ends a stdio server, and
// it is killed if it is still running a moment later.
func (c *MCPClient) Close() error {
	if c == nil || c.cmd == nil {
		return nil
	}
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		return <-done
	}
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *MCPClient) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *MCPClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *MCPClient) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("writing to the server: %w%s", err, c.stderrNote())
	}
	type line struct {
		b   []byte
		err error
	}
	for {
		ch := make(chan line, 1)
		go func() {
			b, err := c.out.ReadBytes('\n')
			ch <- line{b, err}
		}()
		var l line
		select {
		case <-ctx.Done():
			_ = c.cmd.Process.Kill()
			return ctx.Err()
		case l = <-ch:
		}
		if l.err != nil && len(bytes.TrimSpace(l.b)) == 0 {
			return fmt.Errorf("the server closed its output: %w%s", l.err, c.stderrNote())
		}
		var msg rpcMessage
		if err := json.Unmarshal(bytes.TrimSpace(l.b), &msg); err != nil {
			continue // not a JSON-RPC line (a stray log); servers should not, some do
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 {
				// A request from the server (sampling, roots): this client
				// offers none of them.
				_ = c.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "choudoufu offers no client capabilities"}})
			}
			continue
		}
		var got int
		if json.Unmarshal(msg.ID, &got) != nil || got != id {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(msg.Result, out)
	}
}

// lockedBuffer is a buffer the child's stderr copier writes while a failed
// call reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (c *MCPClient) stderrNote() string {
	s := strings.TrimSpace(c.stderr.String())
	if s == "" {
		return ""
	}
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return "\n" + s
}
