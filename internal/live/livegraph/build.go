// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package livegraph

import (
	"context"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/waves"
	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Options is one graph build.
type Options struct {
	// Dir is the root module's directory; source locations are relative
	// to it.
	Dir string
	// Estate is the estate the graph is of.
	Estate string
	// EngineVersion is choudoufu's release, empty on a development build.
	EngineVersion string
	// Plan, when set, is the live-plan document live facts come from.
	Plan *PlanDoc
	// Markers is the marker vocabulary choudoufu passes the public sweeps,
	// and ContinuationKeys the keys a long address continues into.
	Markers          markerkeys.Keys
	ContinuationKeys []string
}

// Built is a document and what -history needs to know about its nodes.
type Built struct {
	Doc *Document
	// records are the node ids a record can exist for: a managed
	// resource's instance address, or its block address when the block
	// and every module call above it are not expanded.
	records map[string]bool
}

// block is one resource or data block of the tree.
type block struct {
	cfg  *configs.Config
	rc   *configs.Resource
	addr string // block address, module path included
	// instances are the instance node ids, when the block or a module
	// call above it expands to known keys. nil otherwise.
	instances []instanceID
	// single is true when neither the block nor any call above it is
	// expanded: the block address is also the one instance's address.
	single bool
}

type instanceID struct {
	id, key, scope string
}

// Build builds the graph of cfg. It reads nothing but cfg and o.Plan.
func Build(cfg *configs.Config, o Options) *Built {
	b := &builder{
		o:     o,
		nodes: map[string]*Node{},
		edges: map[string]Edge{},
		res:   resolver{memo: map[string][]string{}, busy: map[string]bool{}},
	}
	b.collect(cfg)
	b.blockEdges()
	b.crossEstateEdges()
	if o.Plan != nil {
		b.planFacts(o.Plan)
	}

	doc := &Document{Version: 1}
	eg := &doc.Meta.EngineGraph
	eg.Format = 1
	eg.Engine = Engine{Name: "choudoufu", Version: o.EngineVersion}
	eg.Scope = Scope{Kind: "estate", Estate: o.Estate}
	if o.Plan != nil {
		eg.ObservedAt = o.Plan.ObservedAt
	}
	eg.MarkerScheme = &MarkerScheme{
		OwnerKey:         o.Markers.Owner,
		AddressKey:       o.Markers.Address,
		ContinuationKeys: o.ContinuationKeys,
		// choudoufu carries every value as given (markerkeys.Keys.Encode
		// is nil): an address is escaped the same way on a tag and on a
		// label.
		Encoding: MarkerEncoding{Tags: "escaped", Labels: "escaped"},
	}
	sort.Slice(b.gaps, func(i, j int) bool {
		if b.gaps[i].Address != b.gaps[j].Address {
			return b.gaps[i].Address < b.gaps[j].Address
		}
		return b.gaps[i].Reason < b.gaps[j].Reason
	})
	eg.Gaps = b.gaps

	ids := make([]string, 0, len(b.nodes))
	for id := range b.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	doc.Nodes = make([]Node, 0, len(ids))
	for _, id := range ids {
		n := b.nodes[id]
		if n.Facts.empty() {
			n.Facts = nil
		}
		doc.Nodes = append(doc.Nodes, *n)
	}
	keys := make([]string, 0, len(b.edges))
	for k := range b.edges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	doc.Edges = make([]Edge, 0, len(keys))
	for _, k := range keys {
		doc.Edges = append(doc.Edges, b.edges[k])
	}
	return &Built{Doc: doc, records: b.records}
}

type builder struct {
	o       Options
	blocks  []*block
	byAddr  map[string]*block
	nodes   map[string]*Node
	edges   map[string]Edge
	gaps    []Gap
	records map[string]bool
	res     resolver
}

// collect makes a node per block and per known instance.
func (b *builder) collect(root *configs.Config) {
	b.byAddr = map[string]*block{}
	b.records = map[string]bool{}
	root.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		scopes, scoped := moduleInstances(c)
		var rcs []*configs.Resource
		for _, rc := range c.Module.ManagedResources {
			rcs = append(rcs, rc)
		}
		for _, rc := range c.Module.DataResources {
			rcs = append(rcs, rc)
		}
		for _, rc := range rcs {
			blk := &block{cfg: c, rc: rc, addr: rc.Addr().InModule(c.Path).String()}
			b.blocks = append(b.blocks, blk)
			b.byAddr[blk.addr] = blk
			b.addNode(&Node{
				ID: blk.addr, Kind: rc.Type, Lexicon: "terraform",
				Attrs:     NodeAttrs{Address: blk.addr},
				SourceLoc: b.sourceLoc(rc.DeclRange),
			})
			keys, expanded, known := literalKeys(rc.Count, rc.ForEach)
			blk.single = !expanded && !scoped
			if blk.single {
				if rc.Mode == addrs.ManagedResourceMode {
					b.records[blk.addr] = true
				}
				continue
			}
			if !known || scopes == nil {
				// The block stands for instances this read cannot count.
				continue
			}
			if !expanded {
				keys = []addrs.InstanceKey{addrs.NoKey}
			}
			for _, mi := range scopes {
				for _, k := range keys {
					inst := rc.Addr().Instance(k).Absolute(mi)
					iid := instanceID{id: inst.String()}
					if k != addrs.NoKey {
						iid.key = k.String()
					}
					if scoped {
						iid.scope = mi.String()
					}
					blk.instances = append(blk.instances, iid)
					b.addInstanceNode(blk, iid)
				}
			}
		}
	})
	sort.Slice(b.blocks, func(i, j int) bool { return b.blocks[i].addr < b.blocks[j].addr })
}

func (b *builder) addNode(n *Node) *Node {
	if old, ok := b.nodes[n.ID]; ok {
		return old
	}
	b.nodes[n.ID] = n
	return n
}

func (b *builder) addInstanceNode(blk *block, iid instanceID) *Node {
	n := b.addNode(&Node{
		ID: iid.id, Kind: blk.rc.Type, Lexicon: "terraform",
		Attrs:      NodeAttrs{Address: iid.id},
		InstanceOf: blk.addr, InstanceKey: iid.key, InstanceScope: iid.scope,
	})
	if blk.rc.Mode == addrs.ManagedResourceMode {
		b.records[iid.id] = true
	}
	return n
}

func (b *builder) sourceLoc(r hcl.Range) *SourceLoc {
	if r.Filename == "" {
		return nil
	}
	file := r.Filename
	if b.o.Dir != "" {
		if rel, err := filepath.Rel(b.o.Dir, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = rel
		}
	}
	return &SourceLoc{File: filepath.ToSlash(file), Line: r.Start.Line}
}

// fromIDs are the node ids an edge out of blk starts at: the block's own
// node, and each of its instance nodes. An instance reads the block it
// references, never one of that block's instances, so each instance gets
// the block's edges as they are.
func (b *builder) fromIDs(blk *block) []string {
	ids := []string{blk.addr}
	for _, i := range blk.instances {
		ids = append(ids, i.id)
	}
	return ids
}

func (b *builder) addEdge(from, to, via string) {
	if from == to {
		return
	}
	k := from + "\x00" + to + "\x00" + via
	b.edges[k] = Edge{From: from, To: to, Kind: "ref", ViaAttr: via, Grain: GrainBlock}
}

// blockEdges walks each block's body for references (no provider schema;
// the #2275 ruling, Q1) and resolves each through locals, module
// variables and module outputs to the blocks it reads.
func (b *builder) blockEdges() {
	for _, blk := range b.blocks {
		if strings.HasSuffix(blk.rc.DeclRange.Filename, ".json") {
			b.gaps = append(b.gaps, Gap{Address: blk.addr, Reason: GapJSONConfig,
				Detail: "declared in a .tf.json file, whose body cannot be read for references without the provider's schema; its references are not drawn"})
			continue
		}
		refs, ok := bodyRefs(blk.rc.Config)
		if !ok {
			b.gaps = append(b.gaps, Gap{Address: blk.addr, Reason: GapBodyUnread,
				Detail: "the block's body is not native syntax as written (an override file was merged into it), so its references are not drawn"})
			continue
		}
		from := b.fromIDs(blk)
		for _, r := range refs {
			for _, target := range b.res.resolve(blk.cfg, r.key) {
				if _, declared := b.byAddr[target]; !declared {
					continue
				}
				for _, f := range from {
					b.addEdge(f, target, r.via)
				}
			}
		}
	}
}

// crossEstateEdges draws one edge from each data block that reads another
// estate (waves.DataSourceReads' rules: a terraform_estate_outputs block,
// a tag:tofu-estate filter, a tofu-estate tag) to that estate's one node
// (the #2275 ruling, Q2).
func (b *builder) crossEstateEdges() {
	for _, blk := range b.blocks {
		if blk.rc.Mode != addrs.DataResourceMode {
			continue
		}
		estates, err := waves.DataSourceReads(blk.rc)
		if err != nil {
			b.gaps = append(b.gaps, Gap{Address: blk.addr, Reason: GapCrossEstate, Detail: err.Error()})
			continue
		}
		via := "tag:" + markers.TagEstate
		if blk.rc.Type == waves.EstateOutputsTypeName {
			via = "estate"
		}
		for _, e := range estates {
			id := EstateNodeID(e)
			b.addNode(&Node{ID: id, Kind: EstateNodeKind, Lexicon: "terraform", Attrs: NodeAttrs{Estate: e}})
			for _, f := range b.fromIDs(blk) {
				b.addEdge(f, id, via)
			}
		}
	}
}

// planFacts puts the live plan's rows on the nodes they name (chant#4520
// design, section 2, the choudoufu column).
func (b *builder) planFacts(p *PlanDoc) {
	facts := map[string]*Facts{}
	types := map[string]string{}
	var order []string
	get := func(addr, typ string) *Facts {
		f, ok := facts[addr]
		if !ok {
			f = &Facts{ObservedAt: p.ObservedAt}
			facts[addr] = f
			order = append(order, addr)
		}
		if types[addr] == "" {
			types[addr] = typ
		}
		return f
	}
	importID := func(f *Facts, id string) {
		if id == "" {
			return
		}
		for _, c := range f.CloudIDs {
			if c.Value == id {
				return
			}
		}
		f.CloudIDs = append(f.CloudIDs, CloudID{Kind: "import-id", Value: id})
	}
	for _, r := range p.Bound {
		f := get(r.Addr, r.Type)
		f.Ownership = "bound"
		switch r.Source {
		case "marker":
			f.OwnedBy = "marker"
			f.Markers = &Markers{Owner: p.Estate, Address: markers.EscapeAddress(r.Addr), Encoding: "escaped", Source: "live"}
		case "record":
			f.OwnedBy = "record"
		case "derived":
			f.OwnedBy = "name"
		}
		importID(f, r.Identity)
	}
	for _, r := range p.Unowned {
		f := get(r.Addr, r.Type)
		if f.Ownership == "" {
			if r.HeldBy != "" {
				f.Ownership = "foreign"
				f.Markers = &Markers{Owner: r.HeldBy, Encoding: "escaped", Source: "live"}
			} else {
				f.Ownership = "unowned"
			}
		}
		importID(f, r.Identity)
	}
	for _, r := range p.Adoptable {
		f := get(r.Addr, r.Type)
		if f.Ownership == "" {
			f.Ownership = "unowned"
		}
		importID(f, r.Identity)
	}
	for _, r := range p.Omissions {
		if r.Reason != "ABSENT" {
			continue
		}
		f := get(r.Addr, "")
		if f.Ownership == "" {
			f.Ownership = "pending"
		}
	}

	for _, addr := range order {
		f := facts[addr]
		n := b.nodeForAddr(addr, types[addr])
		if n == nil {
			continue
		}
		n.Facts = f
		if len(f.CloudIDs) > 0 {
			n.PhysicalID = f.CloudIDs[0].Value
		}
	}
}

// nodeForAddr is the node a live-plan row's instance address names,
// making the instance node when the configuration could not count it.
func (b *builder) nodeForAddr(addr, typ string) *Node {
	if n, ok := b.nodes[addr]; ok {
		return n
	}
	inst, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		b.gaps = append(b.gaps, Gap{Address: addr, Reason: GapNotDeclared, Detail: "the live plan names an address that does not parse"})
		return nil
	}
	blockAddr := inst.ContainingResource().Config().String()
	blk, ok := b.byAddr[blockAddr]
	if !ok {
		b.gaps = append(b.gaps, Gap{Address: addr, Reason: GapNotDeclared, Detail: "the live plan names an address no block of this configuration declares"})
		if typ == "" {
			typ = inst.Resource.Resource.Type
		}
		n := b.addNode(&Node{ID: addr, Kind: typ, Lexicon: "terraform", Attrs: NodeAttrs{Address: addr}})
		return n
	}
	iid := instanceID{id: addr}
	if inst.Resource.Key != addrs.NoKey {
		iid.key = inst.Resource.Key.String()
	}
	for _, step := range inst.Module {
		if step.InstanceKey != addrs.NoKey {
			iid.scope = inst.Module.String()
			break
		}
	}
	blk.instances = append(blk.instances, iid)
	n := b.addInstanceNode(blk, iid)
	// The new instance reads what its block reads.
	var copied []Edge
	for _, e := range b.edges {
		if e.From == blk.addr {
			copied = append(copied, e)
		}
	}
	for _, e := range copied {
		b.addEdge(addr, e.To, e.ViaAttr)
	}
	return n
}

// HistoryLister lists the versions of one record. kept is false for a
// store that replaces records in place; versions carry no values.
type HistoryLister func(ctx context.Context, addr addrs.AbsResourceInstance) (kept bool, versions []RecordVersion, err error)

// AddHistory sets recordVersions on every node a record can exist for:
// a managed resource's instance (-history, the chant#4520 ruling Q6).
func (bt *Built) AddHistory(ctx context.Context, list HistoryLister) error {
	for i := range bt.Doc.Nodes {
		n := &bt.Doc.Nodes[i]
		if !bt.records[n.ID] {
			continue
		}
		addr, diags := addrs.ParseAbsResourceInstanceStr(n.ID)
		if diags.HasErrors() {
			continue
		}
		kept, versions, err := list(ctx, addr)
		if err != nil {
			return fmt.Errorf("%s: %w", n.ID, err)
		}
		if versions == nil {
			versions = []RecordVersion{}
		}
		if n.Facts == nil {
			n.Facts = &Facts{}
		}
		n.Facts.RecordVersions = &RecordVersions{Kept: kept, Versions: versions}
	}
	return nil
}

// moduleInstances lists the module instances c's blocks live in, and
// whether any call on the path is expanded. nil with scoped true means a
// call above expands to keys this read cannot know.
func moduleInstances(c *configs.Config) ([]addrs.ModuleInstance, bool) {
	scopes := []addrs.ModuleInstance{addrs.RootModuleInstance}
	scoped := false
	known := true
	var chain []*configs.Config
	for x := c; x != nil && x.Parent != nil; x = x.Parent {
		chain = append([]*configs.Config{x}, chain...)
	}
	for _, x := range chain {
		name := x.Path[len(x.Path)-1]
		call := x.Parent.Module.ModuleCalls[name]
		var keys []addrs.InstanceKey
		expanded, ok := false, true
		if call != nil {
			keys, expanded, ok = literalKeys(call.Count, call.ForEach)
		}
		if expanded {
			scoped = true
		}
		if !ok {
			known = false
			continue
		}
		if !expanded {
			keys = []addrs.InstanceKey{addrs.NoKey}
		}
		var next []addrs.ModuleInstance
		for _, s := range scopes {
			for _, k := range keys {
				next = append(next, s.Child(name, k))
			}
		}
		scopes = next
	}
	if !known {
		return nil, true
	}
	return scopes, scoped
}

// literalKeys reads a count or for_each written as a literal: the keys,
// whether the block is expanded at all, and whether the keys are known.
// Anything that needs a variable, a function or another block is unknown
// here; the live plan's rows still name those instances.
func literalKeys(count, forEach hcl.Expression) (keys []addrs.InstanceKey, expanded, known bool) {
	if count != nil {
		v, diags := count.Value(nil)
		if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() || v.Type() != cty.Number {
			return nil, true, false
		}
		n, acc := v.AsBigFloat().Int64()
		if acc != big.Exact || n < 0 || n > 100000 {
			return nil, true, false
		}
		for i := int64(0); i < n; i++ {
			keys = append(keys, addrs.IntKey(int(i)))
		}
		return keys, true, true
	}
	if forEach != nil {
		v, diags := forEach.Value(nil)
		if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
			return nil, true, false
		}
		ty := v.Type()
		switch {
		case ty.IsObjectType() || ty.IsMapType():
			var names []string
			for it := v.ElementIterator(); it.Next(); {
				k, _ := it.Element()
				names = append(names, k.AsString())
			}
			sort.Strings(names)
			for _, s := range names {
				keys = append(keys, addrs.StringKey(s))
			}
			return keys, true, true
		default:
			return nil, true, false
		}
	}
	return nil, false, true
}

// ref is one reference out of a block body: the node key it reads in the
// block's module scope, and the top-level argument or block it sits in.
type ref struct {
	key, via string
}

// bodyRefs is every reference in a native-syntax body, by the top-level
// argument or nested block type it is written in. ok is false for a body
// that is not native syntax.
func bodyRefs(body hcl.Body) ([]ref, bool) {
	sb, ok := body.(*hclsyntax.Body)
	if !ok {
		return nil, false
	}
	seen := map[ref]bool{}
	var out []ref
	add := func(via string, e hcl.Expression) {
		for _, t := range e.Variables() {
			if k := refKey(t); k != "" {
				r := ref{key: k, via: via}
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
		}
	}
	for name, attr := range sb.Attributes {
		add(name, attr.Expr)
	}
	var walk func(via string, body *hclsyntax.Body)
	walk = func(via string, body *hclsyntax.Body) {
		for _, attr := range body.Attributes {
			add(via, attr.Expr)
		}
		for _, blk := range body.Blocks {
			walk(via, blk.Body)
		}
	}
	for _, blk := range sb.Blocks {
		walk(blk.Type, blk.Body)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].key != out[j].key {
			return out[i].key < out[j].key
		}
		return out[i].via < out[j].via
	})
	return out, true
}

// refKey turns a traversal into the module-scope key it reads: a resource
// "aws_x.y", "data.aws_x.y", "local.l", "var.v", "module.m.out" or
// "module.m". "" for what reads no block (count, each, self, path,
// terraform).
func refKey(t hcl.Traversal) string {
	name := func(i int) string {
		if i >= len(t) {
			return ""
		}
		if a, ok := t[i].(hcl.TraverseAttr); ok {
			return a.Name
		}
		return ""
	}
	switch root := t.RootName(); root {
	case "count", "each", "self", "path", "terraform", "tofu":
		return ""
	case "var", "local":
		if n := name(1); n != "" {
			return root + "." + n
		}
	case "module":
		m := name(1)
		if m == "" {
			return ""
		}
		// module.m["k"].out and module.m[0].out keep the output name.
		i := 2
		if i < len(t) {
			if _, isIndex := t[i].(hcl.TraverseIndex); isIndex {
				i++
			}
		}
		if o := name(i); o != "" {
			return "module." + m + "." + o
		}
		return "module." + m
	case "data":
		if a, b := name(1), name(2); a != "" && b != "" {
			return "data." + a + "." + b
		}
	default:
		if n := name(1); n != "" {
			return root + "." + n
		}
	}
	return ""
}

// resolver resolves a module-scope key to the block addresses it reads,
// through locals, a child module's variables (the calling argument in the
// parent) and a module's outputs.
type resolver struct {
	memo map[string][]string
	busy map[string]bool
}

func (r *resolver) resolve(c *configs.Config, key string) []string {
	mk := c.Path.String() + "\x00" + key
	if out, ok := r.memo[mk]; ok {
		return out
	}
	if r.busy[mk] {
		return nil
	}
	r.busy[mk] = true
	out := r.resolveUncached(c, key)
	delete(r.busy, mk)
	sort.Strings(out)
	r.memo[mk] = out
	return out
}

func (r *resolver) exprTargets(c *configs.Config, e hcl.Expression, set map[string]bool) {
	if e == nil {
		return
	}
	for _, t := range e.Variables() {
		if k := refKey(t); k != "" {
			for _, a := range r.resolve(c, k) {
				set[a] = true
			}
		}
	}
}

func (r *resolver) traversalTargets(c *configs.Config, ts []hcl.Traversal, set map[string]bool) {
	for _, t := range ts {
		if k := refKey(t); k != "" {
			for _, a := range r.resolve(c, k) {
				set[a] = true
			}
		}
	}
}

func (r *resolver) resolveUncached(c *configs.Config, key string) []string {
	mod := c.Module
	if mod == nil {
		return nil
	}
	set := map[string]bool{}
	parts := strings.SplitN(key, ".", 3)
	switch parts[0] {
	case "local":
		if l, ok := mod.Locals[parts[1]]; ok {
			r.exprTargets(c, l.Expr, set)
		}
	case "var":
		if c.Parent == nil || c.Parent.Module == nil {
			break
		}
		call := c.Parent.Module.ModuleCalls[c.Path[len(c.Path)-1]]
		if call == nil {
			break
		}
		if sb, ok := call.Config.(*hclsyntax.Body); ok {
			if a, ok := sb.Attributes[parts[1]]; ok {
				r.exprTargets(c.Parent, a.Expr, set)
			}
		}
	case "module":
		child := c.Children[parts[1]]
		if child == nil || child.Module == nil {
			break
		}
		if len(parts) == 3 {
			if o, ok := child.Module.Outputs[parts[2]]; ok {
				r.exprTargets(child, o.Expr, set)
				r.traversalTargets(child, o.DependsOn, set)
			}
			break
		}
		for _, o := range child.Module.Outputs {
			r.exprTargets(child, o.Expr, set)
			r.traversalTargets(child, o.DependsOn, set)
		}
	case "data":
		if rc, ok := mod.DataResources[key]; ok {
			set[rc.Addr().InModule(c.Path).String()] = true
		}
	default:
		if rc, ok := mod.ManagedResources[key]; ok {
			set[rc.Addr().InModule(c.Path).String()] = true
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	return out
}
