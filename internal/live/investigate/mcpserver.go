// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// ServerTool is one tool the choudoufu MCP server lists. Every tool carries
// readOnlyHint or destructiveHint (rule 3 on choudoufu#2280).
type ServerTool struct {
	Name        string
	Description string
	InputSchema map[string]any
	ReadOnly    bool
	Destructive bool

	call func(ctx context.Context, args map[string]any) (*ToolResult, error)
}

func (t ServerTool) listing() map[string]any {
	ann := map[string]any{"readOnlyHint": t.ReadOnly}
	if !t.ReadOnly {
		ann["destructiveHint"] = t.Destructive
	}
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"inputSchema": t.InputSchema,
		"annotations": ann,
	}
}

// ServerConfig is what the server is built from.
type ServerConfig struct {
	Tools   *Tools
	Natives []Native

	// AllowOpApprove passes chant's op-approve through. Off by default.
	AllowOpApprove bool

	// Version is choudoufu's, for serverInfo.
	Version string
}

// Server is the choudoufu MCP server: the native lookups, the template
// catalog, behold's MCP fronted, chant's Ops passed through.
type Server struct {
	tools   []ServerTool
	byName  map[string]ServerTool
	notes   []string
	version string

	Behold *Proxy
	Chant  *Proxy
	cat    Catalog
}

// NewServer builds the server: it reads the template catalog, and starts
// behold mcp and chant serve mcp as children when they are present. A
// neighbour that is absent, or will not start, contributes no tools, and the
// server's instructions say why.
func NewServer(ctx context.Context, cfg ServerConfig) *Server {
	s := &Server{byName: map[string]ServerTool{}, version: cfg.Version}
	t := cfg.Tools
	if t == nil {
		t = &Tools{}
	}

	if behold := t.BeholdPath(); behold != "" {
		p, err := StartProxy(ctx, BeholdPrefix, behold, []string{"mcp", t.dir()}, t.dir(), t.env(), BeholdTools)
		if err != nil {
			s.notes = append(s.notes, fmt.Sprintf("behold's MCP did not start, so no behold: tool is listed and Cypher templates are unavailable: %s", err))
		} else {
			s.Behold = p
		}
	} else {
		s.notes = append(s.notes, "No behold binary on PATH: no behold: tool is listed, and "+GraphTemplatesUnavailable)
	}

	if chant := t.ChantPath(); chant != "" && ChantProject(t.dir()) {
		allow := map[string]hint{}
		for k, v := range ChantTools {
			allow[k] = v
		}
		if cfg.AllowOpApprove {
			allow[ChantOpApprove] = hint{destructive: true}
		}
		p, err := StartProxy(ctx, ChantPrefix, chant, []string{"serve", "mcp"}, t.dir(), t.env(), allow)
		if err != nil {
			s.notes = append(s.notes, fmt.Sprintf("chant serve mcp did not start, so no chant: tool is listed: %s", err))
		} else {
			s.Chant = p
		}
	}

	s.cat = LoadCatalog(ctx, t)
	if s.cat.Unavailable != "" {
		s.notes = append(s.notes, "Investigation templates: "+s.cat.Unavailable)
	}

	for _, n := range cfg.Natives {
		s.add(nativeTool(n))
	}
	s.add(templatesTool(s, t))
	s.add(investigateTool(s, t))
	if s.Behold != nil {
		for _, st := range s.Behold.serverTools() {
			s.add(st)
		}
	}
	if s.Chant != nil {
		for _, st := range s.Chant.serverTools() {
			s.add(st)
		}
	}
	return s
}

func (s *Server) add(t ServerTool) {
	if _, dup := s.byName[t.Name]; dup {
		return
	}
	s.tools = append(s.tools, t)
	s.byName[t.Name] = t
}

// ToolNames are the listed tools' names, sorted.
func (s *Server) ToolNames() []string {
	out := make([]string, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// Tool returns a listed tool.
func (s *Server) Tool(name string) (ServerTool, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// Close ends the children.
func (s *Server) Close() {
	s.Behold.Close()
	s.Chant.Close()
}

// Instructions is the server's initialize instructions.
func (s *Server) Instructions() string {
	parts := []string{
		"choudoufu answers read-only questions about choudoufu estates. Its own tools (owner, history, foreign, affected) are lookups that need no graph; templates lists chant's investigation templates and investigate runs one on a resource. " +
			"behold: tools are behold's MCP, fronted, for graph questions; chant: tools are chant's Ops, passed through, and op-run is the only tool here that changes anything, through chant's gates. No answer carries a record's payload.",
	}
	parts = append(parts, s.notes...)
	return strings.Join(parts, "\n\n")
}

func textResult(v any, isError bool) *ToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": err.Error()})
		isError = true
	}
	return &ToolResult{Content: []ContentItem{{Type: "text", Text: string(b)}}, IsError: isError}
}

func answerResult(a Answer) *ToolResult {
	return textResult(a, a.Refused != "" || a.Unavailable != "")
}

func stringArgs(args map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s is a string", k)
		}
		out[k] = s
	}
	return out, nil
}

func nativeTool(n Native) ServerTool {
	props := map[string]any{}
	var required []string
	for _, in := range n.Inputs {
		props[in.Name] = map[string]any{"type": "string", "description": in.Description}
		if !in.Optional {
			required = append(required, in.Name)
		}
	}
	return ServerTool{
		Name:        n.ID,
		Description: fmt.Sprintf("%s: %s. Reads %s (%s); %s.", n.Title, n.Question, n.Reads, n.Cost, n.Freshness),
		InputSchema: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
		ReadOnly:    true,
		call: func(ctx context.Context, args map[string]any) (*ToolResult, error) {
			in, err := stringArgs(args)
			if err != nil {
				return textResult(map[string]string{"error": err.Error()}, true), nil
			}
			return answerResult(RunNative(ctx, n, in)), nil
		},
	}
}

// TemplateListing is one template as the templates tool and investigate
// -list show it.
type TemplateListing struct {
	Template
	Unavailable string `json:"unavailable,omitempty"`
}

// Listing is the catalog with each template's availability here.
func Listing(cat Catalog, tools *Tools, behold *Proxy) []TemplateListing {
	out := make([]TemplateListing, 0, len(cat.Templates))
	for _, t := range cat.Templates {
		l := TemplateListing{Template: t, Unavailable: Availability(t, tools)}
		if t.Query.Kind == QueryCypher && behold != nil {
			l.Unavailable = ""
			if !behold.Has("ask") {
				l.Unavailable = "graph templates unavailable: behold's MCP lists no ask tool"
			}
		}
		out = append(out, l)
	}
	return out
}

func templatesTool(s *Server, t *Tools) ServerTool {
	return ServerTool{
		Name:        "templates",
		Description: "chant's investigation templates (INTENTIUS/chant#4531), each with whether it can run here: a path template runs through chant search, a Cypher template through behold's graph.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		ReadOnly:    true,
		call: func(ctx context.Context, args map[string]any) (*ToolResult, error) {
			if len(args) > 0 {
				return textResult(map[string]string{"error": "templates takes no arguments"}, true), nil
			}
			return textResult(map[string]any{
				"source":      s.cat.Source,
				"chant":       s.cat.Chant,
				"unavailable": s.cat.Unavailable,
				"templates":   Listing(s.cat, t, s.Behold),
				"natives":     NativesListing(),
			}, false), nil
		},
	}
}

// NativesListing is the native lookups without their functions.
func NativesListing() []Native { return Natives() }

func investigateTool(s *Server, t *Tools) ServerTool {
	return ServerTool{
		Name:        "investigate",
		Description: "Run one of chant's investigation templates on a resource (list them with templates): a path template through chant search, a Cypher template through behold's graph. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"template": map[string]any{"type": "string", "description": "The template id."},
				"node":     map[string]any{"type": "string", "description": "The resource: a name, an IR id, a block address or a cloud ID."},
				"inputs":   map[string]any{"type": "string", "description": "The template's other inputs, as a JSON object of strings ({\"kind\": \"aws_internet_gateway\"})."},
			},
			"required":             []string{"template", "node"},
			"additionalProperties": false,
		},
		ReadOnly: true,
		call: func(ctx context.Context, args map[string]any) (*ToolResult, error) {
			in, err := stringArgs(args)
			if err != nil {
				return textResult(map[string]string{"error": err.Error()}, true), nil
			}
			for k := range in {
				if k != "template" && k != "node" && k != "inputs" {
					return textResult(map[string]string{"error": "investigate takes template, node and inputs, not " + k}, true), nil
				}
			}
			if s.cat.Unavailable != "" {
				return textResult(map[string]string{"error": s.cat.Unavailable}, true), nil
			}
			tpl, ok := s.cat.Template(in["template"])
			if !ok {
				return textResult(map[string]string{"error": fmt.Sprintf("no template %q; templates lists them", in["template"])}, true), nil
			}
			inputs := map[string]string{}
			if raw := in["inputs"]; raw != "" {
				if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
					return textResult(map[string]string{"error": "inputs is a JSON object of strings: " + err.Error()}, true), nil
				}
			}
			inputs[tpl.Resource()] = in["node"]
			if tpl.Query.Kind != QueryCypher {
				return answerResult(RunTemplate(ctx, t, tpl, inputs, nil)), nil
			}
			if s.Behold == nil {
				// Absent, or it would not start: never retried per call.
				return answerResult(RunTemplate(ctx, &Tools{Chant: t.Chant, Behold: "none", Dir: t.Dir, Env: t.Env}, tpl, inputs, nil)), nil
			}
			if !s.Behold.Has("ask") {
				cost, fresh := readsCost(tpl.Reads)
				return answerResult(Answer{Investigation: tpl.ID, Kind: tpl.Query.Kind, Method: MethodDeterministic, Reads: tpl.Reads, Cost: cost, Freshness: fresh, Inputs: inputs, Via: "behold:ask", Unavailable: "graph templates unavailable: behold's MCP lists no ask tool"}), nil
			}
			ask := func(ctx context.Context, args map[string]any) (*ToolResult, error) {
				return s.Behold.Client.CallTool(ctx, "ask", args)
			}
			return answerResult(RunTemplate(ctx, t, tpl, inputs, ask)), nil
		},
	}
}

// Serve runs the server over one stdio pair until r ends.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	in := bufio.NewReaderSize(r, 1<<20)
	var wmu sync.Mutex
	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		line, err := in.ReadBytes('\n')
		if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			var msg rpcMessage
			if jerr := json.Unmarshal([]byte(trimmed), &msg); jerr != nil {
				send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			} else if msg.Method != "" {
				if len(msg.ID) == 0 {
					// A notification: initialized, cancelled. Nothing to say.
				} else {
					wg.Add(1)
					go func(msg rpcMessage) {
						defer wg.Done()
						res, rerr := s.handle(ctx, msg.Method, msg.Params)
						if rerr != nil {
							send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": rerr})
							return
						}
						send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": res})
					}(msg)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		version := ProtocolVersion
		switch p.ProtocolVersion {
		case "2025-06-18", "2025-03-26", "2024-11-05":
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}},
			"serverInfo":      map[string]any{"name": "choudoufu", "version": s.version},
			"instructions":    s.Instructions(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, t.listing())
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "tools/call takes a name and arguments"}
		}
		t, ok := s.byName[p.Name]
		if !ok {
			return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("no tool %q", p.Name)}
		}
		res, err := t.call(ctx, p.Arguments)
		if err != nil {
			return textResult(map[string]string{"error": err.Error()}, true), nil
		}
		return res, nil
	case "prompts/list":
		var list []map[string]any
		for _, p := range Prompts() {
			args := []map[string]any{}
			for _, a := range p.Arguments {
				args = append(args, map[string]any{"name": a.Name, "description": a.Description, "required": a.Required})
			}
			list = append(list, map[string]any{"name": p.Name, "description": p.Description, "arguments": args})
		}
		return map[string]any{"prompts": list}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "prompts/get takes a name and arguments"}
		}
		for _, pr := range Prompts() {
			if pr.Name == p.Name {
				text, err := pr.Render(p.Arguments)
				if err != nil {
					return nil, &rpcError{Code: -32602, Message: err.Error()}
				}
				return map[string]any{
					"description": pr.Description,
					"messages":    []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": text}}},
				}, nil
			}
		}
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("no prompt %q", p.Name)}
	}
	return nil, &rpcError{Code: -32601, Message: fmt.Sprintf("method %s is not served", method)}
}
