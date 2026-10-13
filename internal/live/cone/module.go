// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cone

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

type kind int

const (
	kindOther kind = iota
	kindResource
	kindData
	kindModule
	kindVariable
	kindLocal
	kindOutput
	kindProvider
	kindTerraform
	kindLive
	kindRefactor
)

// block is one node of the root module. Locals are one node per attribute.
type block struct {
	kind   kind
	typ    string // resource or data type
	text   string // normalized source
	source string // module source
	refs   []string
}

func (b block) targetable() bool {
	return b.kind == kindResource || b.kind == kindData || b.kind == kindModule
}

// refactor is one moved, removed or import block.
type refactor struct {
	kind string
	text string
	// addrs are the addresses the block names, to target.
	addrs []string
	// mustPlace are the addresses the head configuration must declare:
	// moved.to and import.to.
	mustPlace []string
}

type module struct {
	blocks    map[string]block
	refactors []refactor
	vars      map[string]string
	nonNative bool
}

func (m *module) hasRefactor(r refactor) bool {
	for _, o := range m.refactors {
		if o.kind == r.kind && o.text == r.text {
			return true
		}
	}
	return false
}

// places reports whether addr, with any instance key dropped, names a
// resource, data or module block of this module.
func (m *module) places(addr string) bool {
	_, ok := m.blocks[stripKeys(addr)]
	return ok
}

// stripKeys drops instance keys: aws_x.y[0] -> aws_x.y, module.m["a"].aws_x.y
// -> module.m (a module-internal address is placed by its module call).
func stripKeys(addr string) string {
	var out []string
	parts := splitTraversal(addr)
	for _, p := range parts {
		if i := strings.IndexByte(p, '['); i >= 0 {
			p = p[:i]
		}
		out = append(out, p)
	}
	if len(out) >= 2 && out[0] == "module" {
		return "module." + out[1]
	}
	if len(out) >= 3 && out[0] == "data" {
		return strings.Join(out[:3], ".")
	}
	if len(out) >= 2 {
		return strings.Join(out[:2], ".")
	}
	return strings.Join(out, ".")
}

// splitTraversal splits on dots outside brackets.
func splitTraversal(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func normalize(b []byte) string {
	return strings.Join(strings.Fields(string(b)), " ")
}

// parseModule reads the root module's native-syntax files and its auto
// tfvars. The second result is the first parse error, or "".
func parseModule(files []file) (*module, string) {
	m := &module{blocks: map[string]block{}, vars: map[string]string{}}
	var firstErr string
	for _, f := range files {
		if strings.HasSuffix(f.name, ".json") {
			if autoVars(strings.TrimSuffix(f.name, ".json")) || !strings.HasSuffix(f.name, ".tfvars.json") {
				m.nonNative = true
			}
			continue
		}
		parsed, diags := hclsyntax.ParseConfig(f.src, f.name, hcl.InitialPos)
		if diags.HasErrors() {
			if firstErr == "" {
				firstErr = diags.Error()
			}
			continue
		}
		body := parsed.Body.(*hclsyntax.Body)
		if strings.HasSuffix(f.name, ".tfvars") {
			if !autoVars(f.name) {
				continue
			}
			for name, attr := range body.Attributes {
				m.vars[name] = normalize(attr.Expr.Range().SliceBytes(f.src))
			}
			continue
		}
		for _, blk := range body.Blocks {
			text := normalize(blk.Range().SliceBytes(f.src))
			switch blk.Type {
			case "resource":
				if len(blk.Labels) == 2 {
					m.blocks[blk.Labels[0]+"."+blk.Labels[1]] = block{kind: kindResource, typ: blk.Labels[0], text: text, refs: bodyRefs(blk.Body)}
				}
			case "data":
				if len(blk.Labels) == 2 {
					m.blocks["data."+blk.Labels[0]+"."+blk.Labels[1]] = block{kind: kindData, typ: blk.Labels[0], text: text, refs: bodyRefs(blk.Body)}
				}
			case "module":
				if len(blk.Labels) == 1 {
					src := ""
					if a, ok := blk.Body.Attributes["source"]; ok {
						if v, d := a.Expr.Value(nil); !d.HasErrors() && v.Type().FriendlyName() == "string" {
							src = v.AsString()
						}
					}
					m.blocks["module."+blk.Labels[0]] = block{kind: kindModule, text: text, source: src, refs: bodyRefs(blk.Body)}
				}
			case "variable":
				if len(blk.Labels) == 1 {
					m.blocks["var."+blk.Labels[0]] = block{kind: kindVariable, text: text}
				}
			case "output":
				if len(blk.Labels) == 1 {
					m.blocks["output."+blk.Labels[0]] = block{kind: kindOutput, text: text, refs: bodyRefs(blk.Body)}
				}
			case "locals":
				for name, attr := range blk.Body.Attributes {
					m.blocks["local."+name] = block{kind: kindLocal, text: normalize(attr.SrcRange.SliceBytes(f.src)), refs: exprRefs(attr.Expr)}
				}
			case "provider":
				key := "provider." + strings.Join(blk.Labels, ".")
				if a, ok := blk.Body.Attributes["alias"]; ok {
					key += "." + normalize(a.Expr.Range().SliceBytes(f.src))
				}
				m.blocks[key] = block{kind: kindProvider, text: text}
			case "terraform":
				prev := m.blocks["terraform"]
				m.blocks["terraform"] = block{kind: kindTerraform, text: prev.text + text}
			case "live":
				m.blocks["live"] = block{kind: kindLive, text: text}
			case "moved", "removed", "import":
				m.refactors = append(m.refactors, refactorOf(blk, f.src, text))
			}
		}
	}
	return m, firstErr
}

func refactorOf(blk *hclsyntax.Block, src []byte, text string) refactor {
	r := refactor{kind: blk.Type, text: text}
	addr := func(name string) string {
		a, ok := blk.Body.Attributes[name]
		if !ok {
			return ""
		}
		return strings.ReplaceAll(normalize(a.Expr.Range().SliceBytes(src)), " ", "")
	}
	switch blk.Type {
	case "moved":
		from, to := addr("from"), addr("to")
		for _, a := range []string{from, to} {
			if a != "" {
				r.addrs = append(r.addrs, stripKeys(a))
			}
		}
		if to != "" {
			r.mustPlace = append(r.mustPlace, to)
		}
	case "removed":
		if from := addr("from"); from != "" {
			r.addrs = append(r.addrs, stripKeys(from))
		}
	case "import":
		if to := addr("to"); to != "" {
			r.addrs = append(r.addrs, stripKeys(to))
			r.mustPlace = append(r.mustPlace, to)
		}
	}
	return r
}

func bodyRefs(body *hclsyntax.Body) []string {
	seen := map[string]bool{}
	_ = hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
		if e, ok := n.(hclsyntax.Expression); ok {
			for _, t := range e.Variables() {
				if r := refOf(t); r != "" {
					seen[r] = true
				}
			}
		}
		return nil
	})
	// depends_on and lifecycle.replace_triggered_by are bare traversals
	// that Variables reports too, so nothing further is needed for them.
	return sortedKeys(seen)
}

func exprRefs(e hclsyntax.Expression) []string {
	seen := map[string]bool{}
	for _, t := range e.Variables() {
		if r := refOf(t); r != "" {
			seen[r] = true
		}
	}
	return sortedKeys(seen)
}

// refOf turns a traversal into the node key it reads, or "".
func refOf(t hcl.Traversal) string {
	name := func(i int) string {
		if i >= len(t) {
			return ""
		}
		if a, ok := t[i].(hcl.TraverseAttr); ok {
			return a.Name
		}
		return ""
	}
	root := t.RootName()
	switch root {
	case "var", "local", "module", "output":
		if n := name(1); n != "" {
			return root + "." + n
		}
	case "data":
		if a, b := name(1), name(2); a != "" && b != "" {
			return "data." + a + "." + b
		}
	case "count", "each", "self", "path", "terraform", "tofu":
		return ""
	default:
		if n := name(1); n != "" {
			return root + "." + n
		}
	}
	return ""
}

// descendants returns from plus every node that reads one of them,
// transitively.
func (m *module) descendants(from map[string]bool) map[string]bool {
	readers := map[string][]string{}
	for k, b := range m.blocks {
		for _, r := range b.refs {
			readers[r] = append(readers[r], k)
		}
	}
	out := map[string]bool{}
	queue := make([]string, 0, len(from))
	for k := range from {
		queue = append(queue, k)
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if out[k] {
			continue
		}
		out[k] = true
		queue = append(queue, readers[k]...)
	}
	return out
}
