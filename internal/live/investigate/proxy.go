// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Prefixes of the tools passed through from a neighbour's MCP server.
const (
	BeholdPrefix = "behold:"
	ChantPrefix  = "chant:"
)

// hint is one passed-through tool's annotations: choudoufu sets them itself
// from this table, whatever the child says, so a client asking for
// confirmation on what changes things asks on the right ones.
type hint struct {
	readOnly    bool
	destructive bool
}

// BeholdTools are the behold MCP tools fronted (ruling Q3 on choudoufu#2280):
// none of them changes anything (show moves the person's open tab).
var BeholdTools = map[string]hint{
	"ask":          {readOnly: true},
	"show":         {readOnly: true},
	"shot":         {readOnly: true},
	"capabilities": {readOnly: true},
}

// ChantTools are chant's Ops tools passed through (choudoufu#2280 "chant
// Ops, passed through"). op-run is the only one that changes anything, and
// it does so through chant's gates. op-approve is not here: it joins only
// when the server is configured to pass it ([ChantOpApprove]), so an agent
// cannot run an Op and approve its own gate in one session by default.
// chant's authoring tools (build, import, scaffold, lint) are never passed.
var ChantTools = map[string]hint{
	"op-list":        {readOnly: true},
	"op-status":      {readOnly: true},
	"op-report":      {readOnly: true},
	"lifecycle-diff": {readOnly: true},
	"op-run":         {destructive: true},
}

// ChantOpApprove is the op-approve pass-through, off by default.
const ChantOpApprove = "op-approve"

// Proxy is one neighbour's MCP server, started as a child, with the curated
// subset of its tools this server re-exports.
type Proxy struct {
	Prefix string
	Client *MCPClient
	Tools  []RemoteTool
	hints  map[string]hint
}

// StartProxy starts bin args in dir, lists its tools and keeps those in
// allow. A tool allow names that the child does not list does not appear;
// nothing is stubbed.
func StartProxy(ctx context.Context, prefix, bin string, args []string, dir string, env []string, allow map[string]hint) (*Proxy, error) {
	client, err := StartMCP(ctx, bin, args, dir, env)
	if err != nil {
		return nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	listed, err := client.ListTools(lctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("listing %s's tools: %w", bin, err)
	}
	p := &Proxy{Prefix: prefix, Client: client, hints: allow}
	for _, t := range listed {
		if _, ok := allow[t.Name]; ok {
			p.Tools = append(p.Tools, t)
		}
	}
	sort.Slice(p.Tools, func(i, j int) bool { return p.Tools[i].Name < p.Tools[j].Name })
	return p, nil
}

// Has reports whether the child listed the tool and it is passed through.
func (p *Proxy) Has(name string) bool {
	if p == nil {
		return false
	}
	for _, t := range p.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// serverTools are the proxy's tools as this server lists them.
func (p *Proxy) serverTools() []ServerTool {
	var out []ServerTool
	for _, rt := range p.Tools {
		rt := rt
		h := p.hints[rt.Name]
		schema := map[string]any{"type": "object"}
		if len(rt.InputSchema) > 0 {
			var s map[string]any
			if json.Unmarshal(rt.InputSchema, &s) == nil {
				schema = s
			}
		}
		out = append(out, ServerTool{
			Name:        p.Prefix + rt.Name,
			Description: rt.Description,
			InputSchema: schema,
			ReadOnly:    h.readOnly,
			Destructive: h.destructive,
			call: func(ctx context.Context, args map[string]any) (*ToolResult, error) {
				return p.Client.CallTool(ctx, rt.Name, args)
			},
		})
	}
	return out
}

// Close ends the child.
func (p *Proxy) Close() {
	if p != nil {
		_ = p.Client.Close()
	}
}
