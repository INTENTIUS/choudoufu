// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"
	"strings"

	"github.com/opentofu/opentofu/internal/addrs"
)

// Class is the identity classification of a single managed resource
// instance. Every resolved instance has exactly one, and the class decides
// which of [Resolution]'s three payload fields is populated.
type Class string

const (
	// ClassConcrete means the import identity is fully computable from
	// configuration; Resolution.ImportID holds it. Admission path 1.
	ClassConcrete Class = "CONCRETE"

	// ClassParentDerived means the identity is a composition of parent
	// resources' live IDs with configuration data;
	// Resolution.Formula holds the composition. Admission path 3.
	ClassParentDerived Class = "PARENT_DERIVED"

	// ClassNeedsDiscovery means the identity is server-assigned and can
	// only be recovered by marker discovery; Resolution.Reason says why.
	// Admission path 2.
	ClassNeedsDiscovery Class = "NEEDS_DISCOVERY"
)

// Resolution is what this package knows about one managed resource
// instance's identity.
//
// Exactly one of ImportID, Formula, and Reason is populated, selected by
// Class. The zero value is not meaningful; resolutions are only produced by
// [Resolve].
type Resolution struct {
	// Addr is the resource instance this resolution describes, including
	// its count or for_each key.
	Addr addrs.AbsResourceInstance

	// Class selects which of the three fields below is populated.
	Class Class

	// ImportID is the provider's import identity string, populated only
	// for ClassConcrete. It is ready to hand to the import path as-is.
	ImportID string

	// Formula is the symbolic identity, populated only for
	// ClassParentDerived. Render it with the parents' live IDs to get an
	// import ID.
	Formula *Formula

	// Reason explains, in one sentence aimed at an operator, why this
	// instance's identity is not in configuration. Populated only for
	// ClassNeedsDiscovery.
	Reason string

	// Undeclared marks a resolution whose resource block is not in the
	// configuration at all: a live resource this estate owns, found by the
	// marker sweep, whose block was deleted. It is never produced by
	// [Resolve], which only ever describes what the configuration declares;
	// marker discovery adds these afterwards.
	//
	// It exists because a projection built from such a resolution has no
	// configuration to read a provider or a dependency set from, and because
	// the alternative - letting the projection builder infer "no block, so
	// this must be a removal" - would turn a genuine mismatch between the
	// resolutions and the configuration, which is a bug worth failing on,
	// into a silent destroy.
	Undeclared bool
}

// Type returns the resource type name, e.g. "aws_route".
func (r Resolution) Type() string {
	return r.Addr.Resource.Resource.Type
}

// String renders a resolution in a single line, for diagnostics and test
// failure output.
func (r Resolution) String() string {
	switch r.Class {
	case ClassConcrete:
		if r.Undeclared {
			return r.Addr.String() + " CONCRETE " + r.ImportID + " UNDECLARED"
		}
		return r.Addr.String() + " CONCRETE " + r.ImportID
	case ClassParentDerived:
		return r.Addr.String() + " PARENT_DERIVED " + r.Formula.String()
	default:
		return r.Addr.String() + " NEEDS_DISCOVERY " + r.Reason
	}
}

// Formula is a parent-derived identity: the recipe for an import ID that
// cannot be written down until the parents' live IDs are known.
//
// The import ID is the concatenation of Parts in order. Rendering is
// deliberately dumb string concatenation: the AWS provider's import-ID
// syntaxes are all "component, separator, component", and the separators
// live in Parts as literals so that nothing downstream has to know which
// resource type uses "_" and which uses "/".
type Formula struct {
	// Parts are the pieces of the import ID, in order.
	Parts []Part

	// Parents lists every parent instance referenced by Parts, deduplicated
	// and sorted by address. It is the dependency set: P1.3 must know all
	// of these live IDs before this instance's identity exists.
	Parents []addrs.AbsResourceInstance
}

// String renders the formula in OpenTofu interpolation syntax, e.g.
// `${aws_route_table.main.id}_0.0.0.0/0`. It is for humans and tests; use
// [Formula.Render] to produce a real import ID.
func (f *Formula) String() string {
	var buf strings.Builder
	for _, p := range f.Parts {
		if p.Parent == nil {
			buf.WriteString(p.Literal)
			continue
		}
		buf.WriteString("${")
		buf.WriteString(p.Parent.Instance.String())
		buf.WriteString(".")
		buf.WriteString(p.Parent.Attr)
		buf.WriteString("}")
	}
	return buf.String()
}

// Render builds the concrete import ID by asking the caller for each
// parent's live value. The lookup receives a parent instance address and
// the identity attribute name being read, and returns the value and
// whether it is known. If any lookup reports the value as unknown, Render
// returns ok == false and the caller must not use the string: a formula
// with a hole in it is not an identity.
func (f *Formula) Render(lookup func(parent addrs.AbsResourceInstance, attr string) (string, bool)) (string, bool) {
	var buf strings.Builder
	for _, p := range f.Parts {
		if p.Parent == nil {
			buf.WriteString(p.Literal)
			continue
		}
		v, ok := lookup(p.Parent.Instance, p.Parent.Attr)
		if !ok {
			return "", false
		}
		buf.WriteString(v)
	}
	return buf.String(), true
}

// Part is one piece of a [Formula]: either a literal fragment of the import
// ID (config data or a separator) or a reference to a parent's live value.
// Exactly one of Literal and Parent is meaningful; Parent == nil selects
// Literal.
type Part struct {
	Literal string
	Parent  *ParentRef
}

// ParentRef is a reference to one identity attribute of one parent resource
// instance: the live value that has to be discovered before a formula can
// be rendered.
type ParentRef struct {
	// Instance is the parent resource instance, with its own expansion key.
	Instance addrs.AbsResourceInstance

	// Attr is the attribute being read, always one of the parent type's
	// IdentityAttrs in the table (in practice "id").
	Attr string
}

// Result is the outcome of resolving a whole configuration: every managed
// resource instance in it, classified.
type Result struct {
	byAddr map[string]Resolution
	order  []string

	// signal is the config-side naming signal collected on the same walk.
	// See [Result.Signal].
	signal *ConfigSignal
}

// Signal is the config-side naming signal for the configuration this result
// was resolved from: which arguments each managed resource instance sets.
//
// It covers every managed resource type in the configuration, not only the
// ones the admission table knows, because its purpose is the question the
// table cannot answer for itself - which types could join it. See
// [ConfigSignal] and [DerivableWith]. A result assembled by hand rather
// than by [Resolve], such as one marker discovery has added to, carries no
// signal and returns nil, which every consumer treats as "nothing to say".
func (res *Result) Signal() *ConfigSignal {
	if res == nil {
		return nil
	}
	return res.signal
}

func newResult() *Result {
	return &Result{byAddr: make(map[string]Resolution)}
}

func (res *Result) add(r Resolution) {
	key := r.Addr.String()
	if _, exists := res.byAddr[key]; !exists {
		res.order = append(res.order, key)
	}
	res.byAddr[key] = r
}

// Get returns the resolution for one instance address.
func (res *Result) Get(addr addrs.AbsResourceInstance) (Resolution, bool) {
	r, ok := res.byAddr[addr.String()]
	return r, ok
}

// Len is the number of resolved instances.
func (res *Result) Len() int {
	return len(res.order)
}

// All returns every resolution, ordered by address.
func (res *Result) All() []Resolution {
	out := make([]Resolution, 0, len(res.order))
	for _, k := range res.order {
		out = append(out, res.byAddr[k])
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr.String() < out[j].Addr.String()
	})
	return out
}

// OfClass returns every resolution in one class, ordered by address.
func (res *Result) OfClass(c Class) []Resolution {
	var out []Resolution
	for _, r := range res.All() {
		if r.Class == c {
			out = append(out, r)
		}
	}
	return out
}

// NeedsDiscovery is the roadmap's needs-discovery list: the instances whose
// identity waits on P2's marker discovery.
func (res *Result) NeedsDiscovery() []Resolution {
	return res.OfClass(ClassNeedsDiscovery)
}
