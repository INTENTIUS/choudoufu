// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func readTemplates(t *testing.T) []Template {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "templates.json"))
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := ParseTemplates(data)
	if err != nil {
		t.Fatalf("chant's own catalog does not parse: %s", err)
	}
	return tpls
}

func templateByID(t *testing.T, id string) Template {
	t.Helper()
	for _, tpl := range readTemplates(t) {
		if tpl.ID == id {
			return tpl
		}
	}
	t.Fatalf("chant's catalog has no template %s", id)
	return Template{}
}

// TestParseTemplatesChantCatalog reads chant#4531's catalog as data, and
// refuses each way a file can break the entry kind, naming the entry.
func TestParseTemplatesChantCatalog(t *testing.T) {
	tpls := readTemplates(t)
	var ids []string
	kinds := map[string]int{}
	for _, tpl := range tpls {
		ids = append(ids, tpl.ID)
		kinds[tpl.Query.Kind]++
	}
	want := []string{"dependents", "dependencies", "paths-to-kind", "capability", "drift", "dependents-all", "dependencies-all", "ownership", "same-object", "readers", "history"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("template ids %v, want %v", ids, want)
	}
	if kinds[QueryPath] != 5 || kinds[QueryCypher] != 6 {
		t.Errorf("want 5 path and 6 Cypher templates, got %v", kinds)
	}

	one := func(entry string) []byte {
		return []byte(`{"version": 1, "templates": [` + entry + `]}`)
	}
	const node = `{"name": "node", "kind": "resource", "description": "The resource."}`
	breaks := map[string]struct {
		doc  []byte
		want string
	}{
		"a Cypher write":     {one(`{"id": "forget", "title": "t", "question": "q", "reads": "session", "inputs": [` + node + `], "query": {"kind": "cypher", "text": "MATCH (r {key: $key}) DETACH DELETE r"}}`), "only reads"},
		"a Cypher splice":    {one(`{"id": "splice", "title": "t", "question": "q", "reads": "session", "inputs": [` + node + `], "query": {"kind": "cypher", "text": "MATCH (r {key: '{{node}}'}) RETURN r"}}`), "never spliced"},
		"no $key":            {one(`{"id": "nokey", "title": "t", "question": "q", "reads": "session", "inputs": [` + node + `], "query": {"kind": "cypher", "text": "MATCH (r) RETURN r"}}`), "$key"},
		"a path $param":      {one(`{"id": "param", "title": "t", "question": "q", "reads": "source", "inputs": [` + node + `], "query": {"kind": "path", "text": "->*$node"}}`), "placeholders"},
		"an unused input":    {one(`{"id": "unused", "title": "t", "question": "q", "reads": "source", "inputs": [` + node + `, {"name": "kind", "kind": "term", "description": "k"}], "query": {"kind": "path", "text": "->*{{node}}"}}`), "never uses kind"},
		"no resource":        {one(`{"id": "nores", "title": "t", "question": "q", "reads": "source", "inputs": [{"name": "kind", "kind": "term", "description": "k"}], "query": {"kind": "path", "text": "kind:{{kind}}"}}`), "one resource input"},
		"a second version":   {[]byte(`{"version": 2, "templates": []}`), "version 2"},
		"an unknown kind":    {one(`{"id": "sql", "title": "t", "question": "q", "reads": "source", "inputs": [` + node + `], "query": {"kind": "sql", "text": "select 1"}}`), "query is"},
		"a twice-defined id": {[]byte(`{"version": 1, "templates": [{"id": "a", "title": "t", "question": "q", "reads": "source", "inputs": [` + node + `], "query": {"kind": "path", "text": "{{node}}"}}, {"id": "a", "title": "t", "question": "q", "reads": "source", "inputs": [` + node + `], "query": {"kind": "path", "text": "{{node}}"}}]}`), "defined twice"},
	}
	for name, b := range breaks {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTemplates(b.doc)
			if err == nil {
				t.Fatalf("the catalog was accepted")
			}
			if !strings.Contains(err.Error(), b.want) {
				t.Errorf("refusal %q does not say %q", err, b.want)
			}
		})
	}
}

// TestLoadCatalog finds the catalog from chant's capabilities document,
// from the templates.json an older chant's package ships, and says so when
// there is no chant.
func TestLoadCatalog(t *testing.T) {
	ctx := context.Background()

	t.Run("from chant search --capabilities --json", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		cat := LoadCatalog(ctx, e.Tools)
		if cat.Unavailable != "" {
			t.Fatalf("unavailable: %s", cat.Unavailable)
		}
		if cat.Source != "chant search --capabilities --json" || cat.Chant != "0.124.53" || len(cat.Templates) != 11 {
			t.Errorf("catalog from %q (chant %q) with %d templates", cat.Source, cat.Chant, len(cat.Templates))
		}
		if got := e.calls(t); len(got) != 1 || got[0] != "chant search --capabilities --json" {
			t.Errorf("chant was run as %v", got)
		}
	})

	t.Run("an older chant: the shipped templates.json", func(t *testing.T) {
		e := newFakeEnv(t)
		pkg := t.TempDir()
		if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name": "@intentius/chant"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{"bin", filepath.Join("src", "capabilities")} {
			if err := os.MkdirAll(filepath.Join(pkg, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		data, _ := os.ReadFile(filepath.Join("testdata", "templates.json"))
		shipped := filepath.Join(pkg, "src", "capabilities", "templates.json")
		if err := os.WriteFile(shipped, data, 0o644); err != nil {
			t.Fatal(err)
		}
		self, _ := os.Executable()
		script := "#!/bin/sh\nINVESTIGATE_FAKE=chant exec '" + self + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(pkg, "bin", "chant.js"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(pkg, "bin", "chant.js"), filepath.Join(e.Bin, "chant")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_BREAK", "old-chant")
		cat := LoadCatalog(ctx, e.Tools)
		if cat.Unavailable != "" {
			t.Fatalf("unavailable: %s", cat.Unavailable)
		}
		resolved, _ := filepath.EvalSymlinks(shipped)
		if got, _ := filepath.EvalSymlinks(cat.Source); got != resolved || len(cat.Templates) != 11 {
			t.Errorf("catalog from %q with %d templates, want %s", cat.Source, len(cat.Templates), shipped)
		}
	})

	t.Run("no chant", func(t *testing.T) {
		e := newFakeEnv(t)
		cat := LoadCatalog(ctx, e.Tools)
		if !strings.Contains(cat.Unavailable, "no chant binary on PATH") || len(cat.Templates) != 0 {
			t.Errorf("want the no-chant message, got %+v", cat)
		}
	})

	t.Run("BREAK: chant's catalog carries a write", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		t.Setenv("FAKE_BREAK", "cypher-write")
		cat := LoadCatalog(ctx, e.Tools)
		if !strings.Contains(cat.Unavailable, "template forget: a Cypher query only reads") || len(cat.Templates) != 0 {
			t.Errorf("a catalog with a deleting template was used: %+v", cat)
		}
	})
}

func resultMatches(t *testing.T, a Answer) []string {
	t.Helper()
	var doc struct {
		Matches []struct {
			ID string `json:"id"`
		} `json:"matches"`
		Rows []struct {
			Key string `json:"key"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(a.Result, &doc); err != nil {
		t.Fatalf("answer result is not JSON: %v\n%s", err, a.Result)
	}
	var out []string
	for _, m := range doc.Matches {
		out = append(out, m.ID)
	}
	for _, r := range doc.Rows {
		out = append(out, r.Key)
	}
	sort.Strings(out)
	return out
}

// TestPathTemplates runs path templates through chant search over the
// fixture estate: each has its expected answer, and a BREAK in chant's
// answer changes it and is caught.
func TestPathTemplates(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		id     string
		inputs map[string]string
		argv   string
		want   []string
	}{
		{"dependents", map[string]string{"node": "aws_s3_bucket.logs"}, "chant search ->*aws_s3_bucket.logs --json", []string{"web/aws_cloudfront_distribution.cdn", "web/aws_s3_bucket_policy.logs"}},
		{"dependencies", map[string]string{"node": "aws_s3_bucket.logs"}, "chant search <-*aws_s3_bucket.logs --json", []string{"web/aws_kms_key.logs"}},
		{"paths-to-kind", map[string]string{"node": "aws_s3_bucket.logs", "kind": "aws_internet_gateway"}, "chant search aws_s3_bucket.logs ->*kind:aws_internet_gateway --json", nil},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			e := newFakeEnv(t, "chant")
			tpl := templateByID(t, c.id)
			a := RunTemplate(ctx, e.Tools, tpl, c.inputs, nil)
			if a.Refused != "" || a.Unavailable != "" {
				t.Fatalf("refused %q, unavailable %q", a.Refused, a.Unavailable)
			}
			if a.Via != "chant search" || a.Kind != KindPath || a.Method != MethodDeterministic || a.Reads != "source" || a.Cost == "" || a.Freshness == "" {
				t.Errorf("answer does not say how it was answered: %+v", a)
			}
			if got := e.calls(t); len(got) != 1 || got[0] != c.argv {
				t.Errorf("chant was run as %v, want [%s]", got, c.argv)
			}
			if got := resultMatches(t, a); !reflect.DeepEqual(got, c.want) {
				t.Errorf("answer %v, want %v", got, c.want)
			}
			if len(c.want) == 0 {
				return
			}
			t.Setenv("FAKE_BREAK", "search-drop")
			broken := RunTemplate(ctx, e.Tools, tpl, c.inputs, nil)
			if got := resultMatches(t, broken); reflect.DeepEqual(got, c.want) {
				t.Errorf("BREAK: chant dropped a match and the answer still reads %v", got)
			}
		})
	}

	t.Run("a scope input becomes --live --env", func(t *testing.T) {
		tpl := templateByID(t, "drift")
		args := chantSearchArgs(tpl, map[string]string{"node": "aws_s3_bucket.logs", "env": "prod"})
		want := []string{"search", "aws_s3_bucket.logs", "--json", "--live", "--env", "prod"}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("args %v, want %v", args, want)
		}
	})

	t.Run("an input that would reshape the query is refused", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		a := RunTemplate(ctx, e.Tools, templateByID(t, "dependents"), map[string]string{"node": "x ->* kind:aws_iam_role"}, nil)
		if !strings.Contains(a.Refused, "one term") {
			t.Errorf("a spaced input was not refused: %+v", a)
		}
		if got := e.calls(t); len(got) != 0 {
			t.Errorf("chant ran for a refused input: %v", got)
		}
	})

	t.Run("no chant: path templates unavailable", func(t *testing.T) {
		e := newFakeEnv(t)
		a := RunTemplate(ctx, e.Tools, templateByID(t, "dependents"), map[string]string{"node": "aws_s3_bucket.logs"}, nil)
		if a.Unavailable != PathTemplatesUnavailable {
			t.Errorf("unavailable %q", a.Unavailable)
		}
	})
}

// TestCypherTemplates sends Cypher templates to behold's ask, and reports
// them unavailable without behold.
func TestCypherTemplates(t *testing.T) {
	ctx := context.Background()

	t.Run("through behold", func(t *testing.T) {
		e := newFakeEnv(t, "behold")
		a := RunTemplate(ctx, e.Tools, templateByID(t, "readers"), map[string]string{"node": "aws_s3_bucket.logs"}, nil)
		if a.Refused != "" || a.Unavailable != "" {
			t.Fatalf("refused %q, unavailable %q", a.Refused, a.Unavailable)
		}
		want := []string{"api/aws_iam_policy.read_logs"}
		if got := resultMatches(t, a); !reflect.DeepEqual(got, want) {
			t.Errorf("readers %v, want %v", got, want)
		}
		calls := e.calls(t)
		wantCalls := []string{"behold mcp " + e.Estate, `behold call ask {"resource":"aws_s3_bucket.logs","template":"readers"}`}
		if !reflect.DeepEqual(calls, wantCalls) {
			t.Errorf("behold calls %q, want %q", calls, wantCalls)
		}

		t.Setenv("FAKE_BREAK", "graph-row")
		broken := RunTemplate(ctx, e.Tools, templateByID(t, "readers"), map[string]string{"node": "aws_s3_bucket.logs"}, nil)
		if got := resultMatches(t, broken); reflect.DeepEqual(got, want) {
			t.Errorf("BREAK: behold returned an extra reader and the answer still reads %v", got)
		}
	})

	t.Run("no behold: graph templates unavailable", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		for _, tpl := range readTemplates(t) {
			if tpl.Query.Kind != QueryCypher {
				continue
			}
			a := RunTemplate(ctx, e.Tools, tpl, map[string]string{"node": "aws_s3_bucket.logs"}, nil)
			if a.Unavailable != GraphTemplatesUnavailable {
				t.Errorf("%s: unavailable %q", tpl.ID, a.Unavailable)
			}
			if Availability(tpl, e.Tools) != GraphTemplatesUnavailable {
				t.Errorf("%s is listed as available with no behold", tpl.ID)
			}
		}
		if got := e.calls(t); len(got) != 0 {
			t.Errorf("something ran: %v", got)
		}
	})
}

// TestInvestigationChildGuard: the investigation path starts chant search
// and behold mcp, and nothing else.
func TestInvestigationChildGuard(t *testing.T) {
	allowed := [][]string{
		{"chant", "search", "->*x", "--json"},
		{"chant", "search", "--capabilities", "--json"},
		{"behold", "mcp", "/dir"},
	}
	for _, a := range allowed {
		if err := investigationChild(a[0], a[1:]); err != nil {
			t.Errorf("%v refused: %s", a, err)
		}
	}
	refused := [][]string{
		{"chant", "serve", "mcp"},
		{"chant", "run", "deploy"},
		{"chant", "approve", "deploy", "gate"},
		{"chant", "build"},
		{"chant", "search", "x", "--fail-on-drift"},
		{"behold", "serve"},
		{"choudoufu", "live-plan"},
		{"chant"},
	}
	for _, a := range refused {
		if err := investigationChild(a[0], a[1:]); err == nil {
			t.Errorf("%v was allowed", a)
		}
	}
}

// TestInvestigationPathCannotReachApply is the done-when's proof that the
// investigation path cannot reach apply: the package imports nothing of
// choudoufu's (so nothing that plans, applies or writes a record), every
// template and lookup on the server is read-only, and running every
// template through the server calls behold's ask and chant's search only.
func TestInvestigationPathCannotReachApply(t *testing.T) {
	t.Run("imports", func(t *testing.T) {
		files, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		checked := 0
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if strings.Contains(p, ".") {
					t.Errorf("%s imports %s: the investigation package imports the standard library only", f, p)
				}
			}
			checked++
		}
		if checked == 0 {
			t.Fatal("no files checked")
		}
	})

	t.Run("every template through the server", func(t *testing.T) {
		e := newFakeEnv(t, "chant", "behold")
		s := NewServer(context.Background(), ServerConfig{Tools: e.Tools, Natives: Natives()})
		defer s.Close()
		inv, ok := s.Tool("investigate")
		if !ok {
			t.Fatal("no investigate tool")
		}
		for _, tpl := range readTemplates(t) {
			inputs := map[string]string{}
			for _, in := range tpl.Inputs[1:] {
				inputs[in.Name] = "x"
			}
			b, _ := json.Marshal(inputs)
			if _, err := inv.call(context.Background(), map[string]any{"template": tpl.ID, "node": "aws_s3_bucket.logs", "inputs": string(b)}); err != nil {
				t.Errorf("%s: %s", tpl.ID, err)
			}
		}
		for _, c := range e.calls(t) {
			switch {
			case strings.HasPrefix(c, "chant search "), strings.HasPrefix(c, "behold call ask "),
				c == "chant serve mcp", strings.HasPrefix(c, "behold mcp "):
				// search, ask, and the two servers' start (chant serve mcp is
				// the Ops pass-through, which the investigate tool never calls).
			default:
				t.Errorf("the investigation path ran %q", c)
			}
			if strings.HasPrefix(c, "chant call ") {
				t.Errorf("the investigation path called a chant Op: %q", c)
			}
		}
	})
}

// TestServerTools: behold's four tools fronted, chant's Ops passed through
// with op-approve off by default, nothing else of either, and every tool
// annotated.
func TestServerTools(t *testing.T) {
	ctx := context.Background()

	t.Run("behold and chant present", func(t *testing.T) {
		e := newFakeEnv(t, "chant", "behold")
		s := NewServer(ctx, ServerConfig{Tools: e.Tools, Natives: Natives()})
		defer s.Close()
		want := []string{"affected", "behold:ask", "behold:capabilities", "behold:show", "behold:shot", "chant:lifecycle-diff", "chant:op-list", "chant:op-report", "chant:op-run", "chant:op-status", "foreign", "history", "investigate", "owner", "templates"}
		if got := s.ToolNames(); !reflect.DeepEqual(got, want) {
			t.Errorf("tools\n  %v\nwant\n  %v", got, want)
		}
		for _, name := range s.ToolNames() {
			tool, _ := s.Tool(name)
			listing := tool.listing()
			ann := listing["annotations"].(map[string]any)
			if _, ok := ann["readOnlyHint"]; !ok {
				t.Errorf("%s has no readOnlyHint", name)
			}
			if name == "chant:op-run" {
				if tool.ReadOnly || !tool.Destructive || ann["destructiveHint"] != true {
					t.Errorf("op-run is not marked destructive: %v", ann)
				}
				continue
			}
			if !tool.ReadOnly {
				t.Errorf("%s changes things, and only chant:op-run may", name)
			}
		}

		// A passed-through call reaches the child unchanged.
		opList, _ := s.Tool("chant:op-list")
		res, err := opList.call(ctx, map[string]any{"runtime": "local"})
		if err != nil || res.IsError || !strings.Contains(res.Text(), `"tool":"op-list"`) {
			t.Errorf("chant:op-list: %v %+v", err, res)
		}
	})

	t.Run("op-approve when configured", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		s := NewServer(ctx, ServerConfig{Tools: e.Tools, Natives: Natives(), AllowOpApprove: true})
		defer s.Close()
		tool, ok := s.Tool("chant:op-approve")
		if !ok || !tool.Destructive {
			t.Errorf("op-approve is not passed through when configured: %v", s.ToolNames())
		}
	})

	t.Run("BREAK: op-approve is caught when it leaks", func(t *testing.T) {
		// The guard above would fail if the default allow list carried
		// op-approve: show that the list is what keeps it out.
		if _, ok := ChantTools[ChantOpApprove]; ok {
			t.Fatal("op-approve is in the default pass-through list")
		}
		leaky := map[string]hint{}
		for k, v := range ChantTools {
			leaky[k] = v
		}
		leaky[ChantOpApprove] = hint{destructive: true}
		e := newFakeEnv(t, "chant")
		p, err := StartProxy(ctx, ChantPrefix, e.Tools.ChantPath(), []string{"serve", "mcp"}, e.Estate, os.Environ(), leaky)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if !p.Has(ChantOpApprove) {
			t.Error("a leaky allow list did not pass op-approve, so the default-off check proves nothing")
		}
	})

	t.Run("behold and chant absent", func(t *testing.T) {
		e := newFakeEnv(t)
		e.Tools.TemplatesFile = filepath.Join("testdata", "templates.json")
		s := NewServer(ctx, ServerConfig{Tools: e.Tools, Natives: Natives()})
		defer s.Close()
		for _, name := range s.ToolNames() {
			if strings.HasPrefix(name, BeholdPrefix) || strings.HasPrefix(name, ChantPrefix) {
				t.Errorf("%s is listed with no behold or chant", name)
			}
		}
		if !strings.Contains(s.Instructions(), GraphTemplatesUnavailable) {
			t.Errorf("the instructions do not say graph templates are unavailable:\n%s", s.Instructions())
		}
		inv, _ := s.Tool("investigate")
		res, _ := inv.call(ctx, map[string]any{"template": "ownership", "node": "arn:aws:s3:::web-logs"})
		if !res.IsError || !strings.Contains(res.Text(), "graph templates unavailable") {
			t.Errorf("a Cypher template without behold: %s", res.Text())
		}
	})

	t.Run("chant present outside a chant project: no Ops", func(t *testing.T) {
		e := newFakeEnv(t, "chant")
		e.Tools.Dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(e.Tools.Dir, "package.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := NewServer(ctx, ServerConfig{Tools: e.Tools, Natives: Natives()})
		defer s.Close()
		for _, name := range s.ToolNames() {
			if strings.HasPrefix(name, ChantPrefix) {
				t.Errorf("%s is listed with no chant project", name)
			}
		}
	})
}

// TestServeProtocol drives the server over stdio the way a client does.
func TestServeProtocol(t *testing.T) {
	e := newFakeEnv(t)
	e.Tools.TemplatesFile = filepath.Join("testdata", "templates.json")
	natives := Natives()
	for i := range natives {
		if natives[i].ID == NativeOwner {
			natives[i].Run = func(_ context.Context, in map[string]string) (any, string, error) {
				return map[string]string{"cloud_id": in["cloud_id"], "estate": "web"}, "2026-10-26T00:00:00Z", nil
			}
		}
	}
	s := NewServer(context.Background(), ServerConfig{Tools: e.Tools, Natives: natives, Version: "test"})
	defer s.Close()

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"owner","arguments":{"cloud_id":"arn:aws:s3:::web-logs"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"safe-to-delete","arguments":{"cloud_id":"arn:aws:s3:::web-logs"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"apply","arguments":{}}}`,
	}
	r := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var w strings.Builder
	if err := s.Serve(context.Background(), r, &w); err != nil {
		t.Fatal(err)
	}
	byID := map[int]map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(w.String()), "\n") {
		var msg map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		var id int
		_ = json.Unmarshal(msg["id"], &id)
		byID[id] = msg
	}
	if len(byID) != 6 {
		t.Fatalf("want six responses (the notification gets none), got %d:\n%s", len(byID), w.String())
	}
	if !strings.Contains(string(byID[1]["result"]), `"name":"choudoufu"`) {
		t.Errorf("initialize: %s", byID[1]["result"])
	}
	if !strings.Contains(string(byID[2]["result"]), `"name":"owner"`) {
		t.Errorf("tools/list: %s", byID[2]["result"])
	}
	var call ToolResult
	_ = json.Unmarshal(byID[3]["result"], &call)
	var ans Answer
	if err := json.Unmarshal([]byte(call.Text()), &ans); err != nil || call.IsError || ans.Investigation != "owner" || ans.ObservedAt == "" || !strings.Contains(string(ans.Result), `"estate":"web"`) {
		t.Errorf("owner over MCP: %v %+v", err, ans)
	}
	if !strings.Contains(string(byID[4]["result"]), "safe-to-delete") {
		t.Errorf("prompts/list: %s", byID[4]["result"])
	}
	got := string(byID[5]["result"])
	for _, want := range []string{"[deterministic]", "[judgment]", `Call owner with {\"cloud_id\": \"arn:aws:s3:::web-logs\"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the guided recipe does not carry %s:\n%s", want, got)
		}
	}
	if _, ok := byID[6]["error"]; !ok {
		t.Errorf("a call to a tool that does not exist (apply) was answered: %s", byID[6]["result"])
	}
}

func TestNativeInputs(t *testing.T) {
	n := Natives()[0]
	n.Run = func(context.Context, map[string]string) (any, string, error) { return nil, "", Refusef("not an ARN") }
	if a := RunNative(context.Background(), n, map[string]string{}); !strings.Contains(a.Refused, "needs cloud_id") {
		t.Errorf("missing input: %+v", a)
	}
	if a := RunNative(context.Background(), n, map[string]string{"cloud_id": "x", "extra": "y"}); !strings.Contains(a.Refused, "no input extra") {
		t.Errorf("extra input: %+v", a)
	}
	if a := RunNative(context.Background(), n, map[string]string{"cloud_id": "x"}); a.Refused != "not an ARN" {
		t.Errorf("refusal: %+v", a)
	}
}
