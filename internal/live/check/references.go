// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/dataread"
)

// Reference is one cross-estate edge visible in configuration alone: a data
// source whose filters name a producer estate's marker tags, the pattern
// live/OUTPUTS.md documents as the replacement for terraform_remote_state
// (banned - see that file's own "The decision"). GitHub issue #790 asks
// live-check to say these rather than leave a reader to infer them from
// source: behold (named in the issue) parses no HCL by design, and this is
// the one edge a split estate exists for.
type Reference struct {
	// From is the consuming data source's own address,
	// "data.<type>.<name>" ([addrs.Resource.String] for a data resource),
	// qualified by Module when it is declared inside one.
	From string

	// Module is the module path From is declared in; empty for the root,
	// the same convention [Site.Module] already uses.
	Module string

	// Estate is the producer estate name the "tag:tofu-estate" filter
	// names - the value that decides whether this counts as a reference at
	// all; see [crossEstateFilters].
	Estate string

	// Address is the producer instance's own address, the
	// "tag:tofu-address" filter's value when the data source sets one.
	// live/OUTPUTS.md's own worked example always sets both, but the issue
	// asks for tag:tofu-estate alone to count, so this is empty rather than
	// guessed when the second filter is absent.
	Address string

	// ReadBy are the resources in this same module that reference From
	// directly, sorted - the planner input the issue's own "Why" section
	// asks for: a move of Address costs one data-source filter rewrite per
	// entry here (tlmig/carve.py, the workbench's planner, has no input for
	// this today).
	ReadBy []string
}

// crossEstateReferences walks cfg's whole static module tree for the
// pattern above, sorted by From so two runs over the same configuration
// agree on order.
func crossEstateReferences(cfg *configs.Config) []Reference {
	if cfg == nil {
		return nil
	}
	var out []Reference
	walkReferenceModules(cfg, &out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].From < out[j].From
	})
	return out
}

// walkReferenceModules is [crossEstateReferences]' recursive step: one
// module's data sources in name order, then its children in name order -
// the same ordering discipline [declaredEstateNamesFrom] and
// [dataread]'s own for_each scan already use, so a rerun of this walk is
// reproducible independent of Go's randomized map iteration.
func walkReferenceModules(cfg *configs.Config, out *[]Reference) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	mod := cfg.Module

	names := make([]string, 0, len(mod.DataResources))
	for name := range mod.DataResources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := mod.DataResources[name]
		estate, address, ok := crossEstateFilters(rc)
		if !ok {
			continue
		}
		*out = append(*out, Reference{
			From:    rc.Addr().String(),
			Module:  cfg.Path.String(),
			Estate:  estate,
			Address: address,
			ReadBy:  readersOf(mod, rc.Addr()),
		})
	}

	childNames := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		walkReferenceModules(cfg.Children[name], out)
	}
}

// crossEstateFilters reads rc's own "filter" blocks - the AWS provider's
// list-filtering shape every producer/consumer pair in live/OUTPUTS.md
// uses - for tag:tofu-estate and tag:tofu-address, each evaluated with no
// context: the pattern's own filter values are literal strings naming
// another estate and address, never an expression this configuration could
// not know standing alone (a live-plan sends them to the provider verbatim,
// the same way it reads a marker tag's own literal value). ok is false when
// no tag:tofu-estate filter is found - the issue's own wording treats that
// one as the deciding filter ("and tag:tofu-address when present").
func crossEstateFilters(rc *configs.Resource) (estate, address string, ok bool) {
	content, _, _ := rc.Config.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "filter"}},
	})
	for _, block := range content.Blocks {
		name, values := staticFilter(block.Body)
		switch name {
		case "tag:tofu-estate":
			if len(values) > 0 {
				estate = values[0]
			}
		case "tag:tofu-address":
			if len(values) > 0 {
				address = values[0]
			}
		}
	}
	return estate, address, estate != ""
}

// staticFilter reads one "filter" block's name and values arguments, the
// AWS provider's own required pair for that block. Neither is decoded
// against a provider schema - this walk runs whether or not one was ever
// read (see [Context.Schemas]) - so a value this configuration builds from
// anything but a literal (a variable, a resource attribute) evaluates to
// nothing and the block is silently not a match: the same "not proven, not
// guessed" rule [rungForType] already follows for a fact this package
// cannot settle offline.
func staticFilter(body hcl.Body) (name string, values []string) {
	content, _, _ := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "name"}, {Name: "values"}},
	})
	nameAttr, ok := content.Attributes["name"]
	if !ok {
		return "", nil
	}
	nameVal, diags := nameAttr.Expr.Value(nil)
	if diags.HasErrors() || nameVal.IsNull() || nameVal.Type() != cty.String {
		return "", nil
	}
	name = nameVal.AsString()

	valuesAttr, ok := content.Attributes["values"]
	if !ok {
		return name, nil
	}
	valuesVal, diags := valuesAttr.Expr.Value(nil)
	if diags.HasErrors() || valuesVal.IsNull() || !valuesVal.CanIterateElements() {
		return name, nil
	}
	for it := valuesVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		if v.Type() == cty.String && !v.IsNull() {
			values = append(values, v.AsString())
		}
	}
	return name, values
}

// readersOf finds every resource in mod (managed or data, other than addr
// itself) whose own body references addr directly, sorted. Only a direct
// reference in the reader's own configuration counts - the "resources in
// this configuration that read it" the issue's own Ask names, not a
// transitive chase through a local value or a module-call argument, which
// #790's scope does not ask for.
func readersOf(mod *configs.Module, addr addrs.Resource) []string {
	var readers []string

	scan := func(candidates map[string]*configs.Resource) {
		names := make([]string, 0, len(candidates))
		for name := range candidates {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rc := candidates[name]
			if rc.Addr().Equal(addr) {
				continue
			}
			if referencesResource(rc, addr) {
				readers = append(readers, rc.Addr().String())
			}
		}
	}
	scan(mod.ManagedResources)
	scan(mod.DataResources)

	sort.Strings(readers)
	return readers
}

// referencesResource reports whether rc's own configuration body contains a
// traversal rooted at addr, walking the raw syntax tree rather than
// decoding against a schema - the same reason [crossEstateFilters] does:
// this runs whether or not a provider schema was ever read, and a data
// source's own filter values are exactly the shape a schema-driven decode
// would need one to enumerate. A body this walk cannot enumerate at all - a
// .tf.json resource, whose body is not [*hclsyntax.Body] - is reported as
// not referencing anything rather than guessed, the same fallback
// [internal/live/dataread]'s managed-argument projection already takes for
// the identical type assertion (managedproj.go's own project method).
func referencesResource(rc *configs.Resource, addr addrs.Resource) bool {
	body, native := rc.Config.(*hclsyntax.Body)
	if !native {
		return false
	}
	for _, trav := range bodyTraversals(body) {
		if trav.RootName() != "data" {
			continue
		}
		ref, diags := addrs.ParseRef(trav)
		if diags.HasErrors() {
			continue
		}
		res, ok := dataread.DataSubject(ref.Subject)
		if !ok {
			continue
		}
		if res.Equal(addr) {
			return true
		}
	}
	return false
}

// bodyTraversals collects every variable traversal in body, at any depth:
// every attribute's own expression, and the same walk repeated into every
// nested block. It has no schema to decode against, on purpose - a resource
// this analysis has no provider schema for still has to be walkable for
// "does this reference that data source", the same requirement
// [internal/live/dataread]'s own for_each scan (analyze.go's
// walkModulesForForEach, forEachDataRefs) already has, and the reason that
// scan also prefers [hclsyntax.Expression.Variables] over a schema-driven
// decode.
func bodyTraversals(body *hclsyntax.Body) []hcl.Traversal {
	var out []hcl.Traversal
	for _, attr := range body.Attributes {
		out = append(out, attr.Expr.Variables()...)
	}
	for _, block := range body.Blocks {
		out = append(out, bodyTraversals(block.Body)...)
	}
	return out
}
