// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package livegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/waves"
	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// GitHub issue #2285's acceptance tests at the package: the document
// decodes against chant's schema, node ids are the ids chant's terraform
// lexicon builds, every edge is block grain and an instance's edge points
// at the block it reads, and cross-estate reads are edges to one node per
// read estate.

var testMarkers = markerkeys.Keys{Owner: "tofu-estate", Address: "tofu-address"}

func loadFixture(t testing.TB, dir string) *configs.Config {
	t.Helper()
	cfg, err := waves.LoadTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readPlan(t testing.TB, path string) *PlanDoc {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p PlanDoc
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func buildSmall(t testing.TB, withPlan bool) *Built {
	t.Helper()
	dir := filepath.Join("testdata", "small")
	o := Options{Dir: dir, Estate: "web", EngineVersion: "0.33.0", Markers: testMarkers,
		ContinuationKeys: []string{"tofu-address-2", "tofu-address-3", "tofu-address-4"}}
	if withPlan {
		o.Plan = readPlan(t, filepath.Join("testdata", "small.live-plan.json"))
	}
	return Build(loadFixture(t, dir), o)
}

func nodeByID(doc *Document) map[string]Node {
	m := map[string]Node{}
	for _, n := range doc.Nodes {
		m[n.ID] = n
	}
	return m
}

// ---------------------------------------------------------------------------
// Schema decode
// ---------------------------------------------------------------------------

// The schema and chant's choudoufu fixture are copies of chant#4527's
// (testdata/chant/SOURCE names the commit). The schema is checked with the
// small validator below, which knows exactly the keywords the generated
// schema uses and refuses any other, so a schema that grows a keyword
// fails here rather than being half-checked.

type schemaDoc struct {
	Defs map[string]any `json:"$defs"`
}

func loadSchema(t *testing.T) schemaDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "chant", "graph.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s schemaDoc
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.Defs["EngineGraphDocument"] == nil {
		t.Fatal("the schema has no EngineGraphDocument definition")
	}
	return s
}

func (s schemaDoc) validate(v any, sch any, path string) []string {
	m, ok := sch.(map[string]any)
	if !ok {
		if b, isBool := sch.(bool); isBool {
			if b {
				return nil
			}
			return []string{path + ": not allowed"}
		}
		return []string{path + ": schema node is not an object"}
	}
	var errs []string
	for k := range m {
		switch k {
		case "description", "title", "$schema", "$id", "$defs", "$ref", "anyOf", "const", "enum", "type",
			"properties", "required", "additionalProperties", "items":
		default:
			errs = append(errs, fmt.Sprintf("%s: schema keyword %q is not one this validator knows", path, k))
		}
	}
	if ref, ok := m["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, ok := s.Defs[name]
		if !ok {
			return append(errs, path+": unknown $ref "+ref)
		}
		errs = append(errs, s.validate(v, def, path)...)
	}
	if alts, ok := m["anyOf"].([]any); ok {
		matched := false
		for _, alt := range alts {
			if len(s.validate(v, alt, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			errs = append(errs, path+": matches no anyOf alternative")
		}
	}
	if c, ok := m["const"]; ok && !reflect.DeepEqual(c, v) {
		errs = append(errs, fmt.Sprintf("%s: %v is not the const %v", path, v, c))
	}
	if enum, ok := m["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if reflect.DeepEqual(e, v) {
				found = true
			}
		}
		if !found {
			errs = append(errs, fmt.Sprintf("%s: %v is not in %v", path, v, enum))
		}
	}
	if ty, ok := m["type"].(string); ok {
		okType := false
		switch ty {
		case "object":
			_, okType = v.(map[string]any)
		case "array":
			_, okType = v.([]any)
		case "string":
			_, okType = v.(string)
		case "boolean":
			_, okType = v.(bool)
		case "number":
			_, okType = v.(float64)
		default:
			errs = append(errs, path+": unknown type "+ty)
		}
		if !okType {
			errs = append(errs, fmt.Sprintf("%s: %T is not a %s", path, v, ty))
			return errs
		}
	}
	if obj, isObj := v.(map[string]any); isObj {
		props, _ := m["properties"].(map[string]any)
		for _, r := range asStrings(m["required"]) {
			if _, ok := obj[r]; !ok {
				errs = append(errs, path+": missing required "+r)
			}
		}
		for k, val := range obj {
			if p, ok := props[k]; ok {
				errs = append(errs, s.validate(val, p, path+"."+k)...)
				continue
			}
			if ap, ok := m["additionalProperties"]; ok {
				errs = append(errs, s.validate(val, ap, path+"."+k)...)
			}
		}
	}
	if arr, isArr := v.([]any); isArr {
		if items, ok := m["items"]; ok {
			for i, e := range arr {
				errs = append(errs, s.validate(e, items, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return errs
}

func asStrings(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (s schemaDoc) check(t *testing.T, doc []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	return s.validate(v, s.Defs["EngineGraphDocument"], "$")
}

func TestDocumentDecodesAgainstChantSchema(t *testing.T) {
	s := loadSchema(t)

	// chant's own fixture passes: the validator accepts what chant writes.
	fixture, err := os.ReadFile(filepath.Join("testdata", "chant", "choudoufu.engine-graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.check(t, fixture); len(errs) > 0 {
		t.Fatalf("chant's fixture fails the validator, so the validator is wrong:\n%s", strings.Join(errs, "\n"))
	}
	// Every field of chant's fixture has a home in this package's types.
	dec := json.NewDecoder(bytes.NewReader(fixture))
	dec.DisallowUnknownFields()
	var round Document
	if err := dec.Decode(&round); err != nil {
		t.Errorf("chant's fixture does not decode into livegraph.Document: %s", err)
	}

	// The validator can fail: a misspelled field on a closed object.
	bad := bytes.Replace(fixture, []byte(`"grain": "block"`), []byte(`"grian": "block"`), 1)
	if errs := s.check(t, bad); len(errs) == 0 {
		t.Fatal("a misspelled edge field passed the validator; the test would prove nothing")
	}

	for _, tc := range []struct {
		name     string
		withPlan bool
		history  bool
	}{
		{"configuration only", false, false},
		{"with -from-plan", true, false},
		{"with -from-plan and -history", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bt := buildSmall(t, tc.withPlan)
			if tc.history {
				err := bt.AddHistory(context.Background(), func(_ context.Context, addr addrs.AbsResourceInstance) (bool, []RecordVersion, error) {
					return true, []RecordVersion{{ID: "v2", WrittenAt: "2026-10-26T11:58:02Z", Current: true}, {ID: "v1", WrittenAt: "2026-10-20T09:14:40Z"}}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			out, err := json.Marshal(bt.Doc)
			if err != nil {
				t.Fatal(err)
			}
			if errs := s.check(t, out); len(errs) > 0 {
				t.Errorf("the document does not decode against chant's schema:\n%s\n%s", strings.Join(errs, "\n"), out)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Node ids
// ---------------------------------------------------------------------------

// chantID maps an engine address to the id chant's terraform lexicon gives
// the same block or instance under root: entityKeyFor's segments
// (lexicons/terraform/src/describe-resources.ts) with each module call's
// instance key kept on its segment, as observedInstanceNodeId does.
func chantID(t *testing.T, root, address string) string {
	t.Helper()
	if inst, diags := addrs.ParseAbsResourceInstanceStr(address); !diags.HasErrors() {
		segs := []string{root}
		for _, step := range inst.Module {
			seg := "module." + step.Name
			if step.InstanceKey != addrs.NoKey {
				seg += step.InstanceKey.String()
			}
			segs = append(segs, seg)
		}
		segs = append(segs, inst.Resource.String())
		return strings.Join(segs, "/")
	}
	t.Fatalf("%s does not parse as an address", address)
	return ""
}

type chantIR struct {
	Nodes []struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		InstanceOf    string `json:"instanceOf"`
		InstanceKey   string `json:"instanceKey"`
		InstanceScope string `json:"instanceScope"`
	} `json:"nodes"`
}

func TestNodeIDsMatchChantIR(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "small.chant-ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ir chantIR
	if err := json.Unmarshal(b, &ir); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{} // chant id -> instanceOf
	for _, n := range ir.Nodes {
		if n.Kind == "Terraform::Resource" || n.Kind == "Terraform::Data" {
			want[n.ID] = n.InstanceOf
		}
	}
	if len(want) < 20 {
		t.Fatalf("the golden IR holds %d resource and data nodes; the fixture is not what this test was written for", len(want))
	}

	bt := buildSmall(t, false)
	got := map[string]string{}
	for _, n := range bt.Doc.Nodes {
		if n.Kind == EstateNodeKind {
			continue
		}
		of := ""
		if n.InstanceOf != "" {
			of = chantID(t, "web", n.InstanceOf)
		}
		got[chantID(t, "web", n.ID)] = of
	}
	var missing, extra []string
	for id, of := range want {
		g, ok := got[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case g != of:
			t.Errorf("%s: instanceOf maps to %q, chant's is %q", id, g, of)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			extra = append(extra, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("node ids differ from chant's IR\nmissing: %v\nextra:   %v", missing, extra)
	}

	// The instance fields are chant's spelling.
	nodes := nodeByID(bt.Doc)
	pool := nodes["module.app[0].aws_eip.pool[1]"]
	if pool.InstanceOf != "module.app.aws_eip.pool" || pool.InstanceKey != "[1]" || pool.InstanceScope != "module.app[0]" {
		t.Errorf("module.app[0].aws_eip.pool[1] = %+v", pool)
	}
	web := nodes["module.app[1].aws_instance.web"]
	if web.InstanceOf != "module.app.aws_instance.web" || web.InstanceKey != "" || web.InstanceScope != "module.app[1]" {
		t.Errorf("module.app[1].aws_instance.web = %+v", web)
	}
	if n := nodes["aws_sqs_queue.bar[1]"]; n.InstanceOf != "aws_sqs_queue.bar" || n.InstanceKey != "[1]" || n.InstanceScope != "" {
		t.Errorf("aws_sqs_queue.bar[1] = %+v", n)
	}
	for _, n := range bt.Doc.Nodes {
		if n.Facts != nil || n.PhysicalID != "" {
			t.Errorf("%s has live facts from a configuration-only build: %+v", n.ID, n)
		}
	}
	if bt.Doc.Meta.EngineGraph.ObservedAt != "" {
		t.Error("a configuration-only build says when it was observed")
	}
}

// ---------------------------------------------------------------------------
// Edges
// ---------------------------------------------------------------------------

func hasEdge(doc *Document, from, to, via string) bool {
	for _, e := range doc.Edges {
		if e.From == from && e.To == to && (via == "" || e.ViaAttr == via) {
			return true
		}
	}
	return false
}

func TestEdgesAreBlockGrain(t *testing.T) {
	for _, withPlan := range []bool{false, true} {
		bt := buildSmall(t, withPlan)
		doc := bt.Doc
		if len(doc.Edges) == 0 {
			t.Fatal("no edges")
		}
		nodes := nodeByID(doc)
		for _, e := range doc.Edges {
			if e.Grain != GrainBlock || e.Kind != "ref" {
				t.Errorf("edge %+v: want kind ref, grain block", e)
			}
			to, ok := nodes[e.To]
			if !ok {
				t.Errorf("edge %+v points at no node", e)
				continue
			}
			if to.InstanceOf != "" {
				t.Errorf("edge %+v points at an instance; block edges point at blocks", e)
			}
			if _, ok := nodes[e.From]; !ok {
				t.Errorf("edge %+v starts at no node", e)
			}
		}

		// count: bar[1] reads foo, the block, never foo[0] or foo[1].
		for _, from := range []string{"aws_sqs_queue.bar", "aws_sqs_queue.bar[0]", "aws_sqs_queue.bar[1]"} {
			if !hasEdge(doc, from, "aws_sqs_queue.foo", "redrive_policy") {
				t.Errorf("no edge %s -> aws_sqs_queue.foo (redrive_policy)", from)
			}
			for _, inst := range []string{"aws_sqs_queue.foo[0]", "aws_sqs_queue.foo[1]"} {
				if hasEdge(doc, from, inst, "") {
					t.Errorf("edge %s -> %s points at an instance", from, inst)
				}
			}
		}

		// Through a module's variable and the root's local, to the bucket;
		// inside the module, an instance under an expanded call reads the
		// block.
		if !hasEdge(doc, "module.app.aws_instance.web", "aws_s3_bucket.logs", "tags") {
			t.Error("no edge module.app.aws_instance.web -> aws_s3_bucket.logs through var.logs_bucket and local.bucket_id")
		}
		if !hasEdge(doc, "module.app[0].aws_eip.pool[1]", "module.app.aws_instance.web", "instance") {
			t.Error("no edge module.app[0].aws_eip.pool[1] -> module.app.aws_instance.web")
		}
		if !hasEdge(doc, "aws_subnet.app", "data.aws_vpc.shared", "vpc_id") {
			t.Error("no edge aws_subnet.app -> data.aws_vpc.shared")
		}
	}
}

func TestCrossEstateReadsAreEdgesToTheEstate(t *testing.T) {
	doc := buildSmall(t, false).Doc
	nodes := nodeByID(doc)
	for _, c := range []struct{ from, estate, via string }{
		{"data.terraform_estate_outputs.net", "network", "estate"},
		{"data.aws_vpc.shared", "core", "tag:tofu-estate"},
	} {
		id := EstateNodeID(c.estate)
		n, ok := nodes[id]
		if !ok || n.Kind != EstateNodeKind || n.Attrs.Estate != c.estate {
			t.Errorf("no estate node %s: %+v", id, n)
		}
		if !hasEdge(doc, c.from, id, c.via) {
			t.Errorf("no edge %s -> %s (%s)", c.from, id, c.via)
		}
	}
	count := 0
	for _, n := range doc.Nodes {
		if n.Kind == EstateNodeKind {
			count++
		}
	}
	if count != 2 {
		t.Errorf("%d estate nodes, want one per read estate (2)", count)
	}

	// A tofu-estate tag on a data source is a read too, and two blocks
	// reading one estate share its node.
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
data "aws_subnet" "a" {
  tags = { tofu-estate = "core" }
}
data "terraform_estate_outputs" "b" {
  estate = "core"
}
data "terraform_estate_outputs" "c" {
  estate = var.which
}
variable "which" {
  type    = string
  default = "x"
}
`)
	doc = Build(loadFixture(t, dir), Options{Dir: dir, Estate: "web", Markers: testMarkers}).Doc
	if !hasEdge(doc, "data.aws_subnet.a", EstateNodeID("core"), "tag:tofu-estate") || !hasEdge(doc, "data.terraform_estate_outputs.b", EstateNodeID("core"), "estate") {
		t.Errorf("tag and estate_outputs reads of core are not both edges to its node: %+v", doc.Edges)
	}
	gap := false
	for _, g := range doc.Meta.EngineGraph.Gaps {
		if g.Address == "data.terraform_estate_outputs.c" && g.Reason == GapCrossEstate {
			gap = true
		}
	}
	if !gap {
		t.Errorf("a read of a non-literal estate is not a gap: %+v", doc.Meta.EngineGraph.Gaps)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestJSONConfigIsAGap: a block in a .tf.json file is a node with no
// outgoing edges and a gap that says why (the #2275 ruling, Q1).
func TestJSONConfigIsAGap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `resource "aws_s3_bucket" "a" { bucket = "a" }`)
	writeFile(t, dir, "extra.tf.json", `{"resource": {"aws_s3_bucket_versioning": {"b": {"bucket": "${aws_s3_bucket.a.id}"}}}}`)
	doc := Build(loadFixture(t, dir), Options{Dir: dir, Estate: "web", Markers: testMarkers}).Doc
	if _, ok := nodeByID(doc)["aws_s3_bucket_versioning.b"]; !ok {
		t.Fatal("the .tf.json block has no node")
	}
	if hasEdge(doc, "aws_s3_bucket_versioning.b", "aws_s3_bucket.a", "") {
		t.Error("a .tf.json block's references were drawn without a schema")
	}
	gaps := doc.Meta.EngineGraph.Gaps
	if len(gaps) != 1 || gaps[0].Address != "aws_s3_bucket_versioning.b" || gaps[0].Reason != GapJSONConfig {
		t.Errorf("gaps = %+v, want one json-config gap", gaps)
	}
}

// ---------------------------------------------------------------------------
// Facts
// ---------------------------------------------------------------------------

func TestPlanFacts(t *testing.T) {
	bt := buildSmall(t, true)
	doc := bt.Doc
	nodes := nodeByID(doc)
	if doc.Meta.EngineGraph.ObservedAt != "2026-10-26T12:00:00Z" {
		t.Errorf("meta observedAt = %q", doc.Meta.EngineGraph.ObservedAt)
	}
	want := map[string]Facts{
		"aws_s3_bucket.logs": {Ownership: "bound", OwnedBy: "record",
			CloudIDs: []CloudID{{Kind: "import-id", Value: "web-logs"}}, ObservedAt: "2026-10-26T12:00:00Z"},
		"module.app[0].aws_eip.pool[1]": {Ownership: "bound", OwnedBy: "marker",
			Markers:  &Markers{Owner: "web", Address: `module.app[0].aws_eip.pool[1]`, Encoding: "escaped", Source: "live"},
			CloudIDs: []CloudID{{Kind: "import-id", Value: "eipalloc-0abc"}}, ObservedAt: "2026-10-26T12:00:00Z"},
		"module.app[0].aws_instance.web": {Ownership: "bound", OwnedBy: "name",
			CloudIDs: []CloudID{{Kind: "import-id", Value: "i-0123"}}, ObservedAt: "2026-10-26T12:00:00Z"},
		"aws_subnet.app": {Ownership: "foreign", Markers: &Markers{Owner: "billing", Encoding: "escaped", Source: "live"},
			CloudIDs: []CloudID{{Kind: "import-id", Value: "subnet-0aa"}}, ObservedAt: "2026-10-26T12:00:00Z"},
		"aws_sqs_queue.foo[0]": {Ownership: "pending", ObservedAt: "2026-10-26T12:00:00Z"},
		"aws_sqs_queue.bar[0]": {Ownership: "unowned",
			CloudIDs: []CloudID{{Kind: "import-id", Value: "https://sqs.us-east-1.amazonaws.com/000000000000/bar-0"}}, ObservedAt: "2026-10-26T12:00:00Z"},
	}
	for id, f := range want {
		n, ok := nodes[id]
		if !ok || n.Facts == nil {
			t.Errorf("%s has no facts", id)
			continue
		}
		if !reflect.DeepEqual(*n.Facts, f) {
			t.Errorf("%s facts = %+v, want %+v", id, *n.Facts, f)
		}
		if len(f.CloudIDs) > 0 && n.PhysicalID != f.CloudIDs[0].Value {
			t.Errorf("%s physicalId = %q, want the first cloud id", id, n.PhysicalID)
		}
	}
	for _, id := range []string{"aws_sqs_queue.foo", "aws_sqs_queue.foo[1]", "data.aws_vpc.shared"} {
		if nodes[id].Facts != nil {
			t.Errorf("%s has facts the plan never gave it: %+v", id, nodes[id].Facts)
		}
	}
	out, _ := json.Marshal(doc)
	for _, never := range []string{`"attribute"`, "adopt_tofu", "NEEDS_DISCOVERY", "the server assigns", "not found"} {
		if bytes.Contains(out, []byte(never)) {
			t.Errorf("the document holds %q from the live plan, which it must not copy", never)
		}
	}
}

// TestHistoryCarriesNoRecordValue: -history puts version ids and times on
// managed instances only, and the lister is never asked for a data block
// or for a block whose instances it cannot address.
func TestHistoryCarriesNoRecordValue(t *testing.T) {
	bt := buildSmall(t, false)
	var asked []string
	err := bt.AddHistory(context.Background(), func(_ context.Context, addr addrs.AbsResourceInstance) (bool, []RecordVersion, error) {
		asked = append(asked, addr.String())
		return true, []RecordVersion{{ID: "v1", WrittenAt: "2026-10-26T11:58:02Z", Current: true}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asked {
		if strings.HasPrefix(a, "data.") || a == "aws_sqs_queue.foo" || a == "module.app.aws_instance.web" {
			t.Errorf("history was listed for %s, which has no record of its own", a)
		}
	}
	nodes := nodeByID(bt.Doc)
	rv := nodes["module.app[1].aws_eip.pool[0]"].Facts
	if rv == nil || rv.RecordVersions == nil || !rv.RecordVersions.Kept || len(rv.RecordVersions.Versions) != 1 || rv.Ownership != "" || rv.ObservedAt != "" {
		t.Errorf("module.app[1].aws_eip.pool[0] facts = %+v, want one version and nothing else", rv)
	}
	if nodes["aws_s3_bucket.logs"].Facts == nil {
		t.Error("an unexpanded managed block got no history")
	}
}
