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
	"strings"
	"unicode"
)

// Kinds of investigation, as an answer names them.
const (
	KindNative = "native"
	KindPath   = "path"
	KindCypher = "cypher"
)

// MethodDeterministic is every investigation this package serves: the answer
// depends only on configuration, records and live reads, and the model only
// picks the investigation and fills its inputs. Guided recipes are MCP
// prompts ([Prompts]) and say where judgment comes in.
const MethodDeterministic = "deterministic"

// Answer is what every investigation prints under -json and returns as an
// MCP tool result: which investigation, how it was answered, what it read
// (its cost and freshness), and the answer itself. Unavailable is set, and
// Result is absent, when the neighbour the investigation needs is missing;
// Refused when the neighbour or the lookup refused the question.
type Answer struct {
	Investigation string            `json:"investigation"`
	Kind          string            `json:"kind"`
	Method        string            `json:"method"`
	Reads         string            `json:"reads"`
	Cost          string            `json:"cost"`
	Freshness     string            `json:"freshness"`
	ObservedAt    string            `json:"observed_at,omitempty"`
	Via           string            `json:"via"`
	Inputs        map[string]string `json:"inputs"`
	Query         string            `json:"query,omitempty"`
	Result        json.RawMessage   `json:"result,omitempty"`
	Unavailable   string            `json:"unavailable,omitempty"`
	Refused       string            `json:"refused,omitempty"`
}

// readsCost says what a template's reads cost and how fresh its answer is.
func readsCost(reads string) (cost, freshness string) {
	switch reads {
	case "source":
		return "configuration only: chant reads the declared source, no cloud call", "as of the working tree"
	case "live":
		return "a live read of the named environment, with the caller's credentials", "as of this run"
	case "snapshots":
		return "recorded snapshots and record metadata, no live read", "as of the newest recorded version"
	case "session":
		return "behold's loaded graph, no cloud call", "as of the newest load into behold's graph"
	}
	return "unknown", "unknown"
}

// BeholdAsk calls behold MCP's ask tool. The investigation path calls no
// other behold tool.
type BeholdAsk func(ctx context.Context, args map[string]any) (*ToolResult, error)

// GraphTemplatesUnavailable is said of every Cypher template when there is
// no behold.
const GraphTemplatesUnavailable = "graph templates unavailable: no behold binary on PATH. Cypher templates run in behold's graph pane (INTENTIUS/behold#1027); install @intentius/behold, or pass -behold=<path>."

// PathTemplatesUnavailable is said of every path template when there is no
// chant to run chant search.
const PathTemplatesUnavailable = "path templates unavailable: no chant binary on PATH to run chant search."

// Availability says whether a template can run here, and why not.
func Availability(t Template, tools *Tools) string {
	switch t.Query.Kind {
	case QueryPath:
		if tools.ChantPath() == "" {
			return PathTemplatesUnavailable
		}
	case QueryCypher:
		if tools.BeholdPath() == "" {
			return GraphTemplatesUnavailable
		}
	}
	return ""
}

// CheckInputs checks the inputs given against a template's: every input
// given, none extra, and no value that could change the query's shape.
func CheckInputs(t Template, inputs map[string]string) error {
	known := map[string]bool{}
	var missing []string
	for _, in := range t.Inputs {
		known[in.Name] = true
		v, ok := inputs[in.Name]
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, in.Name)
			continue
		}
		if strings.IndexFunc(v, unicode.IsSpace) >= 0 || strings.Contains(v, "{{") || strings.Contains(v, "}}") {
			return fmt.Errorf("input %s is one term, with no spaces or braces: %q", in.Name, v)
		}
	}
	var extra []string
	for k := range inputs {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return fmt.Errorf("template %s takes no input %s", t.ID, strings.Join(extra, ", "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("template %s needs %s", t.ID, strings.Join(missing, ", "))
	}
	return nil
}

// ExpandPath fills a path template's placeholders with the inputs. Scope
// inputs are not in the text; they become chant search's --env.
func ExpandPath(t Template, inputs map[string]string) string {
	return placeholder.ReplaceAllStringFunc(t.Query.Text, func(m string) string {
		return inputs[strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}")]
	})
}

// chantSearchArgs is the chant search command line for a path template.
func chantSearchArgs(t Template, inputs map[string]string) []string {
	args := []string{"search", ExpandPath(t, inputs), "--json"}
	for _, in := range t.Inputs {
		if in.Kind == InputScope {
			if t.Reads == "live" {
				args = append(args, "--live")
			}
			args = append(args, "--env", inputs[in.Name])
		}
	}
	return args
}

// RunTemplate answers one template. A path template runs through chant
// search in Tools.Dir; a Cypher template goes to behold's ask (resource plus
// template, behold resolving the resource and binding $key), or is
// Unavailable without behold. ask may be nil, and is then started for this
// one question from Tools.Behold.
func RunTemplate(ctx context.Context, tools *Tools, t Template, inputs map[string]string, ask BeholdAsk) Answer {
	cost, fresh := readsCost(t.Reads)
	a := Answer{
		Investigation: t.ID,
		Kind:          t.Query.Kind,
		Method:        MethodDeterministic,
		Reads:         t.Reads,
		Cost:          cost,
		Freshness:     fresh,
		Inputs:        inputs,
	}
	if err := CheckInputs(t, inputs); err != nil {
		a.Refused = err.Error()
		return a
	}
	switch t.Query.Kind {
	case QueryPath:
		a.Via = "chant search"
		args := chantSearchArgs(t, inputs)
		a.Query = args[1]
		if tools.ChantPath() == "" {
			a.Unavailable = PathTemplatesUnavailable
			return a
		}
		out, err := tools.runChant(ctx, args...)
		if err != nil {
			a.Refused = err.Error()
			return a
		}
		if !json.Valid(out) {
			a.Refused = "chant search --json printed something that is not JSON"
			return a
		}
		a.Result = out
		var doc struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &doc) == nil && doc.Error != nil {
			a.Refused = "chant search refused: " + doc.Error.Message
		}
		return a

	case QueryCypher:
		a.Via = "behold:ask"
		a.Query = t.Query.Text
		if ask == nil {
			behold := tools.BeholdPath()
			if behold == "" {
				a.Unavailable = GraphTemplatesUnavailable
				return a
			}
			if err := investigationChild("behold", []string{"mcp"}); err != nil {
				a.Refused = err.Error()
				return a
			}
			client, err := StartMCP(ctx, behold, []string{"mcp", tools.dir()}, tools.dir(), tools.env())
			if err != nil {
				a.Refused = err.Error()
				return a
			}
			defer client.Close()
			ask = func(ctx context.Context, args map[string]any) (*ToolResult, error) {
				return client.CallTool(ctx, "ask", args)
			}
		}
		args := map[string]any{"resource": inputs[t.Resource()], "template": t.ID}
		rest := map[string]string{}
		for _, in := range t.Inputs[1:] {
			rest[in.Name] = inputs[in.Name]
		}
		if len(rest) > 0 {
			b, _ := json.Marshal(rest)
			args["inputs"] = string(b)
		}
		res, err := ask(ctx, args)
		if err != nil {
			a.Refused = "behold's ask: " + err.Error()
			return a
		}
		text := res.Text()
		if json.Valid([]byte(text)) {
			a.Result = json.RawMessage(text)
		} else {
			b, _ := json.Marshal(text)
			a.Result = b
		}
		if res.IsError {
			a.Refused = "behold refused: " + firstLine(text)
		}
		return a
	}
	a.Refused = fmt.Sprintf("template %s has a query of kind %q", t.ID, t.Query.Kind)
	return a
}

func firstLine(s string) string {
	var doc struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s), &doc) == nil {
		if doc.Error != "" {
			return doc.Error
		}
		if doc.Message != "" {
			return doc.Message
		}
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
