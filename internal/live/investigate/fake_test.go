// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fake chant and behold are this test binary, run through a two-line
// shell script named chant or behold on PATH: TestMain sees
// INVESTIGATE_FAKE and plays that tool instead of running tests. They read
// the fixture estate (testdata/estate) and log every invocation and tool
// call to FAKE_LOG, so a test can say exactly what was run.
//
// FAKE_BREAK injects one fault, so a test shows its check catches it:
//
//	search-drop     chant search drops its last match
//	graph-row       behold's ask returns one more row than the fixture
//	old-chant       chant's capabilities document carries no templates
//	cypher-write    chant's catalog carries a Cypher template that deletes
func TestMain(m *testing.M) {
	switch os.Getenv("INVESTIGATE_FAKE") {
	case "chant":
		os.Exit(fakeChant(os.Args[1:]))
	case "behold":
		os.Exit(fakeBehold(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeEnv is a fixture estate with fake chant and behold on PATH. Either
// can be left out.
type fakeEnv struct {
	Bin     string
	Estate  string
	Log     string
	Tools   *Tools
	present map[string]bool
}

func newFakeEnv(t *testing.T, tools ...string) *fakeEnv {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, tool := range tools {
		script := fmt.Sprintf("#!/bin/sh\nINVESTIGATE_FAKE=%s exec '%s' \"$@\"\n", tool, self)
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// PATH holds only the fakes' directory and the system's, so a real
	// chant or behold on the developer's PATH is never found.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	estate, err := filepath.Abs(filepath.Join("testdata", "estate"))
	if err != nil {
		t.Fatal(err)
	}
	templates, err := filepath.Abs(filepath.Join("testdata", "templates.json"))
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("FAKE_FIXTURE", estate)
	t.Setenv("FAKE_TEMPLATES", templates)
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_BREAK", "")
	e := &fakeEnv{Bin: bin, Estate: estate, Log: log, Tools: &Tools{Dir: estate}, present: map[string]bool{}}
	for _, tool := range tools {
		e.present[tool] = true
	}
	return e
}

// calls are the logged invocations, one per line.
func (e *fakeEnv) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(e.Log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func fakeLog(format string, a ...any) {
	f, err := os.OpenFile(os.Getenv("FAKE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", a...)
}

func fakeChant(args []string) int {
	fakeLog("chant %s", strings.Join(args, " "))
	brk := os.Getenv("FAKE_BREAK")
	switch {
	case len(args) >= 1 && args[0] == "search" && contains(args, "--capabilities"):
		doc := map[string]any{"$schema": "https://intentius.io/chant/schemas/workspace/capabilities/v1/capabilities.schema.json", "contract": 1, "chant": "0.124.53", "capabilities": []any{}, "helpers": []any{}}
		if brk != "old-chant" {
			var tpl struct {
				Templates []map[string]any `json:"templates"`
			}
			data, _ := os.ReadFile(os.Getenv("FAKE_TEMPLATES"))
			_ = json.Unmarshal(data, &tpl)
			if brk == "cypher-write" {
				tpl.Templates = append(tpl.Templates, map[string]any{
					"id": "forget", "title": "Forget it", "question": "remove this node", "reads": "session",
					"inputs": []any{map[string]any{"name": "node", "kind": "resource", "description": "The resource."}},
					"query":  map[string]any{"kind": "cypher", "text": "MATCH (r:Resource {key: $key}) DETACH DELETE r"},
				})
			}
			doc["templates"] = tpl.Templates
		}
		b, _ := json.Marshal(doc)
		fmt.Println(string(b))
		return 0
	case len(args) >= 2 && args[0] == "search":
		var fixture map[string][]json.RawMessage
		data, _ := os.ReadFile(filepath.Join(os.Getenv("FAKE_FIXTURE"), "search.json"))
		_ = json.Unmarshal(data, &fixture)
		matches, ok := fixture[args[1]]
		if !ok {
			b, _ := json.Marshal(map[string]any{"query": args[1], "error": map[string]any{"code": "query-invalid", "message": "no fixture for " + args[1]}})
			fmt.Println(string(b))
			return 1
		}
		if brk == "search-drop" && len(matches) > 0 {
			matches = matches[:len(matches)-1]
		}
		if matches == nil {
			matches = []json.RawMessage{}
		}
		b, _ := json.Marshal(map[string]any{"query": args[1], "source": map[string]any{"kind": "declared"}, "matches": matches})
		fmt.Println(string(b))
		return 0
	case len(args) == 2 && args[0] == "serve" && args[1] == "mcp":
		return fakeMCP("chant", []string{"op-list", "op-status", "op-report", "lifecycle-diff", "op-run", "op-approve", "build", "import", "scaffold"}, func(name string, a map[string]any) (any, bool) {
			return map[string]any{"tool": name, "arguments": a}, false
		})
	}
	fmt.Fprintf(os.Stderr, "fake chant: unexpected %v\n", args)
	return 2
}

func fakeBehold(args []string) int {
	fakeLog("behold %s", strings.Join(args, " "))
	if len(args) < 1 || args[0] != "mcp" {
		fmt.Fprintf(os.Stderr, "fake behold: unexpected %v\n", args)
		return 2
	}
	return fakeMCP("behold", []string{"ask", "show", "shot", "capabilities", "index-write"}, func(name string, a map[string]any) (any, bool) {
		if name != "ask" {
			return map[string]any{"tool": name, "arguments": a}, false
		}
		tpl, _ := a["template"].(string)
		var fixture map[string][]json.RawMessage
		data, _ := os.ReadFile(filepath.Join(os.Getenv("FAKE_FIXTURE"), "graph.json"))
		_ = json.Unmarshal(data, &fixture)
		rows, ok := fixture[tpl]
		if !ok {
			return map[string]any{"error": "no template " + tpl}, true
		}
		if os.Getenv("FAKE_BREAK") == "graph-row" {
			rows = append(rows, json.RawMessage(`{"key":"injected/aws_s3_bucket.extra"}`))
		}
		return map[string]any{"template": tpl, "resource": a["resource"], "inputs": a["inputs"], "rows": rows}, false
	})
}

// fakeMCP is a minimal stdio MCP server listing names and answering each
// call with answer.
func fakeMCP(server string, names []string, answer func(string, map[string]any) (any, bool)) int {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": server, "version": "fake"}}
		case "tools/list":
			var tools []map[string]any
			for _, n := range names {
				tools = append(tools, map[string]any{"name": n, "description": server + " " + n, "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}})
			}
			result = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			b, _ := json.Marshal(p.Arguments)
			fakeLog("%s call %s %s", server, p.Name, b)
			v, isErr := answer(p.Name, p.Arguments)
			text, _ := json.Marshal(v)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "isError": isErr}
		default:
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "not served"}})
			continue
		}
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
