// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// CheckResidueAttributes warns, once per resource block and attribute path,
// when a configuration sets an argument whose value can never round-trip a
// stateless replan: a write-only attribute, whose value the plugin protocol
// forbids the provider ever returning, or - under
// `strict { secrets = "refuse" }` only - a sensitive settable attribute,
// which that setting keeps out of every marker and record even when a cloud
// read would echo it. Either way no memory of the value survives a run, so
// every stateless plan re-proposes sending it - exactly what stock
// `terraform import` produces for the same arguments. The configuration
// still works; the warning exists so the perpetual diff is set knowingly
// rather than discovered.
//
// GitHub issue #126's ruling: a warning, never a refusal. A refusal fails at
// both ends of the measurement - it cannot see the schema-invisible members
// at all, and among the schema-visible ones seven sensitive attributes are
// unconditionally required, so refusing the argument would refuse the type
// and undo its admission (an aws_lightsail_database without master_password
// is not a valid configuration).
//
// The verdict is derived from the live provider schema at runtime -
// [configschema.Attribute] carries WriteOnly and Sensitive through
// GetProviderSchema, the same decode path issue #126's wo-sweep probe
// measured through - so there is no generated table to drift, and a new
// provider release's new write-only twin is covered the day it ships.
//
// # What it cannot see, by construction
//
// The founding example, aws_s3_object.content, stays UNCAUGHT: its schema
// reads optional/not-sensitive/not-write-only, indistinguishable from any
// ordinary argument, and that the provider's Read never fetches an object
// body is provider behavior the schema carries no trace of. The same holds
// for the `_wo_version` bookkeeping companions. The LIMITATIONS entry names
// both shapes; a schema-driven check has nothing to hang them on.
//
// Two smaller static boundaries, both resolved toward silence because a
// warning that fires on an attribute the author never set would train
// authors to ignore it: content inside `dynamic` blocks is invisible to
// this pass (the block body is a template, not the block), and a nested
// object attribute assigned anything but a literal object constructor
// cannot be looked inside without evaluation, so only statically visible
// keys are matched there.
//
// # Not an [Issue], not a [Rule], and not in any registry
//
// Lint issues are fatal by design ([Diagnostics] hardcodes hcl.DiagError),
// and #126 ruled this a warning, so it rides the tfdiags channel from each
// live entry point, beside [CheckWith] - the wiring the retired
// CheckModuleProviders warning used (#70). The refusal registries do not
// want it either, honestly: internal/live/refusalscan scans the identity,
// passthrough, stamp, discovery and projection packages, not lint (lint's
// registry is ruleInfo, whose contract - TestEveryRuleConstantIsRegistered
// - covers Rule constants), and internal/live/check's sweep counts Analyze
// issues, which warnings are not. Its documentation obligation is met the
// same way the rules meet theirs: the hand-written LIMITATIONS entry this
// warning's detail cites.
//
// A nil cfg, empty schemas, or a configuration setting none of the flagged
// attributes returns nil.
func CheckResidueAttributes(cfg *configs.Config, schemas map[string]providers.Schema) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	// GitHub issue #365 slice 3. The sensitive half of this warning is a
	// claim about what a record may hold, and that claim is now the
	// operator's to make: under `strict { secrets = "store" }`, the default,
	// internal/live/projection's residue mechanism records a sensitive
	// settable argument the same way it records an ordinary one, so the
	// sentence "no memory of the value survives a run" would be false.
	//
	// Read once, from the root, through the same function every other layer
	// reads it with. The write-only half is unaffected and stays: no setting
	// makes a write-only value returnable.
	walkResidueAttributes(cfg, schemas, identity.SecretsFor(cfg), &diags)
	return diags
}

// walkResidueAttributes visits every module node, root first then children
// in name order, warning over each managed resource whose type has a schema.
// A type with no schema (the effects types, or a run whose providers gave no
// schemas) is silently skipped: with nothing to consult there is nothing to
// claim.
func walkResidueAttributes(cfg *configs.Config, schemas map[string]providers.Schema, secrets strict.Secrets, diags *tfdiags.Diagnostics) {
	if cfg == nil || cfg.Module == nil {
		return
	}

	names := make([]string, 0, len(cfg.Module.ManagedResources))
	for name := range cfg.Module.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		resource := cfg.Module.ManagedResources[name]
		schema, ok := schemas[resource.Type]
		if !ok || schema.Block == nil {
			continue
		}
		addr := resource.Addr().String()
		if !cfg.Path.IsRoot() {
			addr = cfg.Path.String() + "." + addr
		}
		warnResidueInBody(addr, resource.Config, schema.Block, "", secrets, diags)
	}

	childNames := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		walkResidueAttributes(cfg.Children[name], schemas, secrets, diags)
	}
}

// warnResidueInBody warns about every flagged attribute one body sets, then
// recurses into the nested blocks the body actually contains. Attributes
// are visited in name order and blocks in source order, so repeated runs
// over one configuration warn identically.
func warnResidueInBody(addr string, body hcl.Body, block *configschema.Block, prefix string, secrets strict.Secrets, diags *tfdiags.Diagnostics) {
	if body == nil || block == nil {
		return
	}

	bodySchema := &hcl.BodySchema{}
	attrNames := make([]string, 0, len(block.Attributes))
	for name := range block.Attributes {
		attrNames = append(attrNames, name)
		bodySchema.Attributes = append(bodySchema.Attributes, hcl.AttributeSchema{Name: name})
	}
	sort.Strings(attrNames)
	blockNames := make([]string, 0, len(block.BlockTypes))
	for name := range block.BlockTypes {
		blockNames = append(blockNames, name)
		bodySchema.Blocks = append(bodySchema.Blocks, hcl.BlockHeaderSchema{Type: name})
	}
	sort.Strings(blockNames)

	// PartialContent rather than Content: the body still carries whatever
	// this schema does not name (unknown arguments, dynamic blocks, and at
	// top level anything internal/configs left behind), and none of that is
	// this warning's business. Its diagnostics are dropped for the same
	// reason lint's for_each check drops evalStatic's: a body this pass
	// cannot scan is not this pass's finding to report.
	content, _, _ := body.PartialContent(bodySchema)
	if content == nil {
		return
	}

	for _, name := range attrNames {
		attr, set := content.Attributes[name]
		if !set {
			continue
		}
		schemaAttr := block.Attributes[name]
		if kind, flagged := residueFlag(schemaAttr, secrets); flagged {
			*diags = diags.Append(residueWarning(addr, prefix+name, kind, attr.Expr.Range()))
			continue
		}
		if schemaAttr.NestedType != nil {
			warnResidueInObjectExpr(addr, attr.Expr, schemaAttr.NestedType, prefix+name+".", secrets, diags)
		}
	}

	for _, inner := range content.Blocks {
		bt, ok := block.BlockTypes[inner.Type]
		if !ok {
			continue
		}
		warnResidueInBody(addr, inner.Body, &bt.Block, prefix+inner.Type+".", secrets, diags)
	}
}

// warnResidueInObjectExpr matches the statically visible keys of an object
// constructor expression against a nested object schema, descending through
// list, set and map nesting when the collection is itself written out
// literally. Anything it cannot see into - a variable reference, a splat, a
// function call - is left silent on purpose; see the doc comment's static
// boundary.
func warnResidueInObjectExpr(addr string, expr hcl.Expression, obj *configschema.Object, prefix string, secrets strict.Secrets, diags *tfdiags.Diagnostics) {
	if obj == nil {
		return
	}

	switch obj.Nesting {
	case configschema.NestingList, configschema.NestingSet:
		elems, listDiags := hcl.ExprList(expr)
		if listDiags.HasErrors() {
			return
		}
		single := configschema.Object{Attributes: obj.Attributes, Nesting: configschema.NestingSingle}
		for _, elem := range elems {
			warnResidueInObjectExpr(addr, elem, &single, prefix, secrets, diags)
		}
		return
	case configschema.NestingMap:
		pairs, mapDiags := hcl.ExprMap(expr)
		if mapDiags.HasErrors() {
			return
		}
		single := configschema.Object{Attributes: obj.Attributes, Nesting: configschema.NestingSingle}
		for _, pair := range pairs {
			warnResidueInObjectExpr(addr, pair.Value, &single, prefix, secrets, diags)
		}
		return
	}

	pairs, mapDiags := hcl.ExprMap(expr)
	if mapDiags.HasErrors() {
		return
	}
	for _, pair := range pairs {
		// A naked identifier key evaluates to its own name with a nil
		// EvalContext (hclsyntax's ObjectConsKeyExpr handles the keyword
		// case itself); a computed key errors instead and is skipped, per
		// the static boundary above.
		keyVal, keyDiags := pair.Key.Value(nil)
		if keyDiags.HasErrors() || !keyVal.IsKnown() || keyVal.IsNull() || keyVal.Type() != cty.String {
			continue
		}
		key := keyVal.AsString()
		nested, ok := obj.Attributes[key]
		if !ok {
			continue
		}
		if kind, flagged := residueFlag(nested, secrets); flagged {
			*diags = diags.Append(residueWarning(addr, prefix+key, kind, pair.Value.Range()))
			continue
		}
		if nested.NestedType != nil {
			warnResidueInObjectExpr(addr, pair.Value, nested.NestedType, prefix+key+".", secrets, diags)
		}
	}
}

// residueFlag reports whether one schema attribute belongs to the class this
// warning covers, and which of the two reasons applies. Write-only wins the
// label when both flags are set, because it is the stronger claim and the
// unconditional one: the protocol itself forbids the value coming back,
// whatever the secrets setting says.
func residueFlag(a *configschema.Attribute, secrets strict.Secrets) (string, bool) {
	if a == nil || (!a.Required && !a.Optional) {
		// A computed-only attribute cannot be set in configuration, so
		// there is nothing to warn about even when it is sensitive: that is
		// the aws_iam_access_key.secret shape (#125), a created-once export
		// rather than a perpetually re-proposed argument.
		return "", false
	}
	switch {
	case a.WriteOnly:
		return residueWriteOnly, true
	case a.Sensitive && !strict.StoresSecrets(secrets):
		// Only under the setting that makes the claim true. See
		// [CheckResidueAttributes]; under the default this argument is an
		// ordinary residue candidate and there is nothing to warn about.
		return residueSensitive, true
	}
	return "", false
}

const (
	residueWriteOnly = "write-only"
	residueSensitive = "sensitive"
)

// residueWarning is the one diagnostic this check raises, naming the
// resource, the attribute path, and which of the two reasons the value
// cannot round-trip.
func residueWarning(addr, path, kind string, subject hcl.Range) *hcl.Diagnostic {
	var reason string
	switch kind {
	case residueWriteOnly:
		reason = "the provider's schema marks it write-only, so the plugin protocol forbids the provider ever returning its value"
	case residueSensitive:
		reason = `the provider's schema marks it sensitive and this estate's live block sets strict { secrets = "refuse" }, which keeps its value out of every ownership marker and record`
	}
	return &hcl.Diagnostic{
		Severity: hcl.DiagWarning,
		Summary:  "Attribute value cannot round-trip a stateless replan",
		Detail: fmt.Sprintf(
			"%s sets %q, and %s. No memory of the value survives a run, so every stateless plan will propose sending it again - the same perpetual diff stock `terraform import` produces for this argument. The plan is correct and the apply converges; set the value knowingly. See live/LIMITATIONS.md, \"Attribute-level residue\" (GitHub issue #126).",
			addr, path, reason,
		),
		Subject: subject.Ptr(),
	}
}
