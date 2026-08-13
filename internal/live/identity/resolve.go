// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Resolve classifies every managed resource instance in the given
// configuration, using only the configuration itself: no provider process,
// no state, no cloud reads.
//
// The returned Result holds one Resolution per instance that could be
// classified. An instance that could not be classified is absent from the
// Result and has at least one error diagnostic explaining why; callers must
// treat error diagnostics as fatal for the run, since a projection built
// from a partial identity map would plan to create resources that already
// exist.
//
// Input variable values come from the configuration's own static module
// call, i.e. whatever the caller passed to the loader, falling back to
// declared defaults.
func Resolve(ctx context.Context, cfg *configs.Config) (*Result, tfdiags.Diagnostics) {
	return ResolveWith(ctx, cfg, Context{})
}

// Context is everything a caller may tell resolution about the world outside
// the configuration. Every field is optional and the zero value is what
// [Resolve] passes: nothing here is ever fetched, discovered, or guessed, and
// a caller with none of it gets the same answers this package has always
// given.
//
// The two fields are the same shape of escape hatch for the same reason. A
// configuration does not say which account it will be applied to, and it does
// not carry the provider's account of what identifies each resource type;
// both live behind a running provider, and resolution runs in front of one.
// So each is handed in by a caller that already has it, and its absence is an
// ordinary answer rather than an error.
type Context struct {
	// Cloud is the account and region the run is against. See
	// [CloudContext].
	Cloud CloudContext

	// Schemas are the provider's managed resource type schemas, keyed by
	// type name, as GetProviderSchema returns them. They let a type absent
	// from [DefaultTable] resolve anyway, when the provider's own resource
	// identity schema describes it completely enough. See
	// [SynthesizeTypeIdentity] for the rule and for what it refuses.
	//
	// Nil is the default and means the hand table is the whole of what this
	// run knows, which is what every caller running before a plugin has
	// started passes.
	Schemas map[string]providers.Schema
}

// ResolveWith is [Resolve] told everything the caller knows that the
// configuration does not carry. See [Context].
func ResolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, rctx)
}

// ResolveIn is [Resolve] told which cloud the run is against, so that the
// types whose import identity embeds the account or the region can be
// computed rather than deferred. See [CloudContext] for what those values
// are and where a caller gets them.
//
// A caller that does not have them passes the zero value, which is exactly
// what [Resolve] does: every type needing a value the context does not carry
// classifies [ClassNeedsDiscovery], naming the missing property, and marker
// discovery finds it instead. That fallback is the reason this parameter can
// exist at all without making the package need a provider - nothing here
// ever fetches a cloud property, and nothing here fails for want of one.
func ResolveIn(ctx context.Context, cfg *configs.Config, cloud CloudContext) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, Context{Cloud: cloud})
}

func resolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if cfg == nil || cfg.Module == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "No configuration to resolve",
			Detail:   "Identity resolution was given an empty configuration.",
		})
		return newResult(), diags
	}
	if len(cfg.Children) > 0 {
		// Defense in depth, not the user's explanation: lint's RuleChildModule
		// rejects module calls with a range and a forwarding address, and it
		// runs before this in both commands. Reaching here means the pipeline
		// ran out of order.
		names := make([]string, 0, len(cfg.Children))
		for name := range cfg.Children {
			names = append(names, name)
		}
		sort.Strings(names)
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Configuration with child modules reached identity resolution",
			Detail: fmt.Sprintf(
				"Live resource markers v0 cover the root module only, and this configuration calls %s. Lint rejects module calls before this point, so this is a bug in the live-markers pipeline.",
				strings.Join(quoteAll(names), ", "),
			),
		})
		return newResult(), diags
	}
	if cfg.Module.StaticEvaluator == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Configuration loaded without a static evaluator",
			Detail:   "Identity resolution evaluates configuration expressions through the module's static evaluator, which this configuration does not have. Load the configuration with configs.Parser.LoadConfigDir or the configload package.",
		})
		return newResult(), diags
	}

	r := newResolver(ctx, cfg, rctx)

	result := newResult()
	// Collected for the whole configuration before anything is classified,
	// and for every type rather than only the admitted ones. Two reasons: the
	// config-side naming signal is what says which types could join the
	// table, so a type the table has never heard of is the interesting case
	// (signal.go); and the fallback that lets such a type resolve at all
	// consults the signal for its verdict, which has to be the whole
	// configuration's answer rather than however much of it the walk happens
	// to have reached. See [SynthesizeTypeIdentity].
	result.signal = r.collectSignal(cfg)
	r.signal = result.signal
	for _, rc := range sortedResources(cfg.Module.ManagedResources) {
		exp, ok := r.expansionFor(rc)
		if !ok {
			continue
		}
		for _, key := range exp.keys {
			addr := rc.Addr().Instance(key).Absolute(addrs.RootModuleInstance)
			res, ok := r.instance(addr, rc.DeclRange)
			if !ok {
				continue
			}
			result.add(res)
		}
	}

	r.checkCollisions(result)

	return result, r.diags
}

// checkCollisions reports two instances of the same type that resolve to
// the same identity. They would bind to one live resource, so the plan
// would treat one cloud object as two managed resources, the same class of
// ambiguity the marker path treats as a named error rather than picking a
// winner.
func (r *resolver) checkCollisions(result *Result) {
	seen := make(map[string]addrs.AbsResourceInstance)
	for _, res := range result.All() {
		var ident string
		switch res.Class {
		case ClassConcrete:
			ident = res.ImportID
		case ClassParentDerived:
			ident = res.Formula.String()
		default:
			continue
		}
		key := res.Type() + "\x00" + ident

		first, exists := seen[key]
		if !exists {
			seen[key] = res.Addr
			continue
		}

		rng := hcl.Range{}
		if rc := r.mod.ResourceByAddr(res.Addr.Resource.Resource); rc != nil {
			rng = rc.DeclRange
		}
		r.errorf(rng, "Two resources with the same identity",
			"%s and %s both resolve to the identity %q. Both would bind to the same live resource, so one of them has to change: an identity is what tells a live-markers run which cloud object a configuration block owns.",
			first.String(), res.Addr.String(), ident)
	}
}

func newResolver(ctx context.Context, cfg *configs.Config, rctx Context) *resolver {
	return &resolver{
		ctx:     ctx,
		mod:     cfg.Module,
		cloud:   rctx.Cloud,
		schemas: rctx.Schemas,
		// Pure on purpose: an identity is a claim about which cloud object a
		// block owns, and a function that answers differently every time it
		// is called cannot make that claim. See impure.go.
		eval:       cfg.Module.StaticEvaluator.Pure(),
		expansions: make(map[string]*expansion),
		expFailed:  make(map[string]bool),
		expVisit:   make(map[string]bool),
		insts:      make(map[string]Resolution),
		instFailed: make(map[string]bool),
		instVisit:  make(map[string]bool),
		synth:      make(map[string]*TypeIdentity),
	}
}

type resolver struct {
	ctx   context.Context
	mod   *configs.Module
	eval  *configs.StaticEvaluator
	cloud CloudContext
	diags tfdiags.Diagnostics

	// schemas are the provider's resource type schemas when the caller had
	// them, and nil when it did not. See [Context.Schemas].
	schemas map[string]providers.Schema

	// signal is the whole configuration's naming signal, collected before
	// classification starts because the schema fallback's verdict depends on
	// it. Nil for the signal-only walk of [ScanConfig], which classifies
	// nothing.
	signal *ConfigSignal

	// synth memoizes the schema fallback per type, including its refusals: a
	// nil entry means "asked, and the schemas do not describe this type well
	// enough". See [SynthesizeTypeIdentity].
	synth map[string]*TypeIdentity

	// Expansion memo, keyed by resource address (no instance key).
	expansions map[string]*expansion
	expFailed  map[string]bool
	expVisit   map[string]bool

	// Instance resolution memo, keyed by absolute instance address.
	insts      map[string]Resolution
	instFailed map[string]bool
	instVisit  map[string]bool
}

// lookupType is [LookupType] with the schema fallback behind it: a type the
// hand table does not cover still resolves when the provider's own identity
// schema describes it completely and the caller supplied the schemas. A
// caller that supplied none gets [LookupType] exactly.
func (r *resolver) lookupType(typeName string) (TypeIdentity, bool) {
	if entry, ok := LookupType(typeName); ok {
		return entry, true
	}
	if memo, asked := r.synth[typeName]; asked {
		if memo == nil {
			return TypeIdentity{}, false
		}
		return *memo, true
	}

	entry, ok := SynthesizeTypeIdentity(typeName, r.schemas, r.signal)
	if !ok {
		r.synth[typeName] = nil
		return TypeIdentity{}, false
	}
	r.synth[typeName] = &entry
	return entry, true
}

// instance resolves one managed resource instance, memoizing the result.
// rng is the source range to blame for the request: the resource's own
// declaration when called from the top-level walk, or the referencing
// expression when called for a parent.
func (r *resolver) instance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	key := addr.String()
	if res, ok := r.insts[key]; ok {
		return res, true
	}
	if r.instFailed[key] {
		return Resolution{}, false
	}
	if r.instVisit[key] {
		r.errorf(rng, "Circular identity reference",
			"The identity of %s depends on itself, directly or through other resources. Identity cannot be resolved for a cycle.", key)
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.instVisit[key] = true
	defer delete(r.instVisit, key)

	res, ok := r.resolveInstance(addr, rng)
	if !ok {
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.insts[key] = res
	return res, true
}

func (r *resolver) resolveInstance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	resAddr := addr.Resource.Resource

	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		r.errorf(rng, "Reference to undeclared resource",
			"%s is not declared in this configuration, so its identity cannot be resolved.", resAddr.String())
		return Resolution{}, false
	}

	// The referenced instance key has to be one this resource actually
	// expands to; otherwise a reference like aws_subnet.this.id (whole
	// resource, no key) would silently resolve against a nonexistent
	// NoKey instance.
	exp, ok := r.expansionFor(rc)
	if !ok {
		return Resolution{}, false
	}
	if !exp.hasKey(addr.Resource.Key) {
		r.errorf(rng, "Reference to a resource instance that does not exist",
			"%s does not exist. %s", addr.String(), exp.describe(resAddr))
		return Resolution{}, false
	}

	entry, ok := r.lookupType(resAddr.Type)
	if !ok {
		r.errorf(rng, "Resource type outside the live-markers subset",
			"There is no identity knowledge for resource type %q, so %s cannot be admitted to a live-markers projection. "+
				"The v0 identity table covers: %s.%s See the roadmap's \"The admission rule\".",
			resAddr.Type, addr.String(), strings.Join(AdmittedTypes(), ", "), r.schemaRefusal(resAddr.Type))
		return Resolution{}, false
	}

	if entry.ServerAssigned {
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: entry.Reason,
		}, true
	}

	// Cloud properties are checked before anything is read from the resource
	// body, so that a type this run cannot name gets the one honest answer -
	// "the account is not known here" - rather than an error about some
	// argument that would have been fine had the account been known.
	if missing, ok := r.missingCloudValue(entry); ok {
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: cloudReason(entry, missing),
		}, true
	}

	attrs, ok := r.identityArgs(rc, entry)
	if !ok {
		return Resolution{}, false
	}

	scope := exp.scope(addr.Resource.Key)
	var parts []Part
	// The same pieces again, split by the identity attribute each component
	// supplies. It is what makes an import ask the provider for
	// {"role": …, "policy_arn": …} rather than for the two joined by a "/"
	// that only this table knows about. Attributes are kept in component
	// order so that a rendered formula reads the way the import syntax does.
	byAttr := make(map[string][]Part)
	var attrOrder []string
	addTo := func(name string, got []Part) {
		if name == "" {
			return
		}
		if _, seen := byAttr[name]; !seen {
			attrOrder = append(attrOrder, name)
		}
		byAttr[name] = append(byAttr[name], got...)
	}

	for _, comp := range entry.Components {
		if comp.Cloud != CloudNone {
			// Present: missingCloudValue already refused the alternative.
			v, _ := r.cloud.value(comp.Cloud)
			got := []Part{{Literal: v}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		if len(comp.Attrs) == 0 {
			got := []Part{{Literal: comp.Literal}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		attr := firstPresent(attrs, comp.Attrs)
		if attr == nil {
			r.errorf(rc.DeclRange, "Identity argument not set",
				"%s has no value for %s, so its import identity (%s) cannot be built.",
				addr.String(), orList(comp.Attrs), entry.ImportSyntax)
			return Resolution{}, false
		}
		ident := r.identifier(addr, attr.Name, attr.Range)
		got, ok := r.resolveExpr(attr.Expr, scope, ident)
		if !ok {
			return Resolution{}, false
		}
		parts = append(parts, got...)
		addTo(comp.identityAttrFor(attr.Name), got)
	}

	return classify(addr, coalesce(parts), attrFormulas(byAttr, attrOrder)), true
}

// attrFormulas turns the per-attribute part lists into the ordered form a
// [Formula] carries, each one coalesced the way the whole import ID is.
func attrFormulas(byAttr map[string][]Part, order []string) []AttrFormula {
	if len(order) == 0 {
		return nil
	}
	out := make([]AttrFormula, 0, len(order))
	for _, name := range order {
		out = append(out, AttrFormula{Name: name, Parts: coalesce(byAttr[name])})
	}
	return out
}

// classify turns a resolved part list into a Resolution: concrete if every
// part is a literal, parent-derived if any part waits on a live value. The
// per-attribute split follows the same fork, since it is the same parts.
func classify(addr addrs.AbsResourceInstance, parts []Part, attrs []AttrFormula) Resolution {
	var parents []addrs.AbsResourceInstance
	seen := make(map[string]bool)
	for _, p := range parts {
		if p.Parent == nil {
			continue
		}
		k := p.Parent.Instance.String()
		if !seen[k] {
			seen[k] = true
			parents = append(parents, p.Parent.Instance)
		}
	}

	if len(parents) == 0 {
		var buf strings.Builder
		for _, p := range parts {
			buf.WriteString(p.Literal)
		}
		var values map[string]string
		if len(attrs) > 0 {
			values = make(map[string]string, len(attrs))
			for _, a := range attrs {
				var v strings.Builder
				for _, p := range a.Parts {
					v.WriteString(p.Literal)
				}
				values[a.Name] = v.String()
			}
		}
		return Resolution{
			Addr:           addr,
			Class:          ClassConcrete,
			ImportID:       buf.String(),
			IdentityValues: values,
		}
	}

	sort.Slice(parents, func(i, j int) bool {
		return parents[i].String() < parents[j].String()
	})
	return Resolution{
		Addr:  addr,
		Class: ClassParentDerived,
		Formula: &Formula{
			Parts:   parts,
			Attrs:   attrs,
			Parents: parents,
		},
	}
}

// missingCloudValue reports the first cloud property this entry's identity
// needs that the run was not given, in component order.
func (r *resolver) missingCloudValue(entry TypeIdentity) (CloudValue, bool) {
	for _, comp := range entry.Components {
		if comp.Cloud == CloudNone {
			continue
		}
		if _, ok := r.cloud.value(comp.Cloud); !ok {
			return comp.Cloud, true
		}
	}
	return CloudNone, false
}

// cloudReason is the needs-discovery explanation for an identity that embeds
// a property of the cloud rather than of the configuration. It has to read
// as an operator-facing sentence, because it is printed as one: the
// projection's omission section quotes it verbatim.
func cloudReason(entry TypeIdentity, missing CloudValue) string {
	return fmt.Sprintf(
		"an %s imports by %s, which embeds the %s, and that is a property of the cloud this run is pointed at rather than of the configuration; nothing has told this run what it is, so the identity cannot be built here.",
		entry.Type, entry.ImportSyntax, missing.describe(),
	)
}

// identityArgs pulls just the arguments the type's identity needs out of
// the resource body. Everything else in the body, including nested blocks,
// is ignored: identity resolution has no business decoding a whole
// resource.
func (r *resolver) identityArgs(rc *configs.Resource, entry TypeIdentity) (hcl.Attributes, bool) {
	var names []string
	seen := make(map[string]bool)
	for _, comp := range entry.Components {
		for _, n := range comp.Attrs {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)

	schema := &hcl.BodySchema{}
	for _, n := range names {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: n})
	}

	content, _, diags := rc.Config.PartialContent(schema)
	if diags.HasErrors() {
		r.diags = r.diags.Append(diags)
		return nil, false
	}
	return content.Attributes, true
}

// resolveExpr turns one argument expression into import-ID parts.
func (r *resolver) resolveExpr(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	if !r.isSymbolic(expr, scope) {
		val, ok := r.evalStatic(expr, scope, ident)
		if !ok {
			return nil, false
		}
		s, ok := r.stringValue(val, expr, ident)
		if !ok {
			return nil, false
		}
		return []Part{{Literal: s}}, true
	}

	switch e := expr.(type) {
	case *hclsyntax.TemplateExpr:
		var parts []Part
		for _, sub := range e.Parts {
			got, ok := r.resolveExpr(sub, scope, ident)
			if !ok {
				return nil, false
			}
			parts = append(parts, got...)
		}
		return parts, true
	case *hclsyntax.TemplateWrapExpr:
		return r.resolveExpr(e.Wrapped, scope, ident)
	case *hclsyntax.ParenthesesExpr:
		return r.resolveExpr(e.Expression, scope, ident)
	}

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		r.errorf(expr.Range(), "Identity not resolvable from configuration",
			"%s refers to another resource inside an expression that identity resolution cannot follow. "+
				"A resource reference contributes to an identity only as a whole reference or as an interpolation in a string template; "+
				"it cannot be passed through functions or operators, because the value it produces is not known until apply.",
			ident.Subject)
		return nil, false
	}
	return r.resolveTraversal(trav, scope, ident)
}

// resolveTraversal turns a reference to another resource's attribute into a
// single part: a literal when that parent is already concrete, a parent
// reference otherwise.
func (r *resolver) resolveTraversal(trav hcl.Traversal, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	rng := trav.SourceRange()

	if trav.RootName() == "each" && scope.eachParent != nil {
		// each.value in a for_each-over-a-resource block is the parent
		// instance with the same key, so each.value.id is that parent's
		// id.
		if len(trav) != 3 || !isAttrStep(trav[1], "value") {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		attrStep, ok := trav[2].(hcl.TraverseAttr)
		if !ok {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		parent := scope.eachParent.Instance(scope.key).Absolute(addrs.RootModuleInstance)
		return r.parentPart(parent, attrStep.Name, rng, ident)
	}

	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.diags = r.diags.Append(refDiags)
		return nil, false
	}

	var instAddr addrs.ResourceInstance
	switch subject := ref.Subject.(type) {
	case addrs.Resource:
		instAddr = subject.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		instAddr = subject
	default:
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, which is not a managed resource. An identity can only be composed from configuration values and other managed resources' identities.",
			ident.Subject, ref.Subject.String())
		return nil, false
	}
	if instAddr.Resource.Mode != addrs.ManagedResourceMode {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s. Data sources are read at plan time and are not part of the live-markers identity model.",
			ident.Subject, instAddr.String())
		return nil, false
	}
	if len(ref.Remaining) != 1 {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, but an identity can only be built from a single attribute of another resource (its identity attribute).",
			ident.Subject, traversalString(trav))
		return nil, false
	}
	attrStep, ok := ref.Remaining[0].(hcl.TraverseAttr)
	if !ok {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s indexes into %s. An identity can only be built from a single attribute of another resource.",
			ident.Subject, instAddr.String())
		return nil, false
	}

	return r.parentPart(instAddr.Absolute(addrs.RootModuleInstance), attrStep.Name, rng, ident)
}

func (r *resolver) parentPart(parent addrs.AbsResourceInstance, attrName string, rng hcl.Range, ident configs.StaticIdentifier) ([]Part, bool) {
	parentRes, ok := r.instance(parent, rng)
	if !ok {
		r.errorf(rng, "Unresolvable identity",
			"%s depends on the identity of %s, which could not be resolved (see the other error).",
			ident.Subject, parent.String())
		return nil, false
	}

	entry, _ := r.lookupType(parent.Resource.Resource.Type)
	if !entry.hasIdentityAttr(attrName) {
		detail := fmt.Sprintf(
			"%s reads %s.%s, but %q is not an identity attribute of %s. ",
			ident.Subject, parent.String(), attrName, attrName, parent.Resource.Resource.Type)
		if len(entry.IdentityAttrs) == 0 {
			detail += fmt.Sprintf("No attribute of %s carries its import identity, so nothing about it can be recovered without reading the cloud.", parent.Resource.Resource.Type)
		} else {
			detail += fmt.Sprintf("Only %s can be resolved without reading the cloud; every other attribute is known only after apply.", orList(entry.IdentityAttrs))
		}
		r.errorf(rng, "Not an identity attribute", "%s", detail)
		return nil, false
	}

	if parentRes.Class == ClassConcrete {
		// The parent's identity is already a string, so this stays
		// concrete rather than becoming a formula.
		return []Part{{Literal: parentRes.ImportID}}, true
	}
	return []Part{{Parent: &ParentRef{Instance: parent, Attr: attrName}}}, true
}

// isSymbolic reports whether an expression references something whose value
// this package refuses to evaluate and instead handles structurally: a
// managed resource, or each.value when for_each iterates over a resource.
func (r *resolver) isSymbolic(expr hcl.Expression, scope instScope) bool {
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "each":
			if scope.eachParent != nil && len(trav) >= 2 && isAttrStep(trav[1], "value") {
				return true
			}
		case "count", "var", "local", "path", "terraform", "module", "data", "self":
			// Not symbolic: either statically evaluable or a case
			// evalStatic will reject with its own message.
		default:
			// Anything else in a resource argument is a managed resource
			// reference; whether it is declared is checked later.
			return true
		}
	}
	return false
}

// evalStatic evaluates an expression that references no managed resources,
// through the module's static evaluator plus a child scope carrying
// each/count for the instance being resolved.
func (r *resolver) evalStatic(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if names := impureCallsIn(expr); len(names) > 0 {
		r.errorf(expr.Range(), "Identity derived from an impure function",
			"%s calls %s, which returns a different value every time it is evaluated. "+
				"An identity is the answer to \"which live object does this block own\", and a value that changes between runs cannot answer it: "+
				"the first apply would create a resource under a name nothing can compute again, and every plan after it would propose creating another one. "+
				"Nothing detects that afterwards, because each run's fabricated identity looks like a perfectly ordinary one. "+
				"Pass the value in as a variable, or let the cloud assign the name and let the tofu-address marker record the ownership.",
			ident.Subject, orListQuoted(names))
		return cty.NilVal, false
	}

	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() {
		r.diags = r.diags.Append(diags)
		return cty.NilVal, false
	}
	return val, true
}

// evalPure is the evaluation itself, with its diagnostics handed back
// rather than recorded. [resolver.evalStatic] records them, because an
// argument that will not evaluate is a resolution failure; the config-side
// naming signal (signal.go) discards them, because an argument it cannot
// read is still an argument the configuration sets.
func (r *resolver) evalPure(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	var travs []hcl.Traversal
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "each", "count":
			// Supplied by the instance scope below; the static evaluator
			// panics on repetition references, so they must not reach it.
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, diags.Append(refDiags)
	}

	hclCtx, ctxDiags := r.eval.EvalContext(r.ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, diags.Append(ctxDiags)
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(scope.vars) > 0 {
		child := hclCtx.NewChild()
		child.Variables = scope.vars
		hclCtx = child
	}

	val, valDiags := expr.Value(hclCtx)
	if valDiags.HasErrors() {
		return cty.NilVal, diags.Append(valDiags)
	}
	return val, diags
}

func (r *resolver) stringValue(val cty.Value, expr hcl.Expression, ident configs.StaticIdentifier) (string, bool) {
	if val.IsMarked() {
		r.errorf(expr.Range(), "Identity derived from a sensitive value",
			"%s is derived from a sensitive value. An import identity is written to logs and plan output, so it cannot be sensitive.", ident.Subject)
		return "", false
	}
	if val.IsNull() {
		r.errorf(expr.Range(), "Null identity argument",
			"%s evaluated to null, which cannot be part of an import identity.", ident.Subject)
		return "", false
	}
	if !val.IsWhollyKnown() {
		r.errorf(expr.Range(), "Non-static identity argument",
			"%s cannot be evaluated from configuration alone. Every part of an identity must be a constant, or derived from variables, locals and functions, or a reference to another resource's identity attribute.", ident.Subject)
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		r.errorf(expr.Range(), "Non-string identity argument",
			"%s cannot be used as part of an import identity: %s.", ident.Subject, err)
		return "", false
	}
	return str.AsString(), true
}

func (r *resolver) identifier(addr addrs.AbsResourceInstance, attrName string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("%s.%s", addr.String(), attrName),
		DeclRange: rng,
	}
}

func (r *resolver) errorf(rng hcl.Range, summary, format string, args ...any) {
	r.diags = r.diags.Append(&hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  rng.Ptr(),
	})
}

// ---- expansion -------------------------------------------------------

// expansion is how a resource block expands into instances, plus whatever
// each instance needs in scope to evaluate its own arguments.
type expansion struct {
	keys []addrs.InstanceKey

	// counted is set when the expansion came from count, so that
	// count.index is in scope.
	counted bool

	// eachValues holds each.value per key for a for_each over a static
	// collection. Nil when for_each iterates over another resource.
	eachValues map[addrs.InstanceKey]cty.Value

	// eachParent is set when for_each iterates over another managed
	// resource: each.value is then that resource's instance with the same
	// key, which is a symbolic reference rather than a value.
	eachParent *addrs.Resource
}

func (e *expansion) hasKey(key addrs.InstanceKey) bool {
	for _, k := range e.keys {
		if k == key {
			return true
		}
	}
	return false
}

// describe explains an expansion in an error about a bad instance
// reference.
func (e *expansion) describe(res addrs.Resource) string {
	if len(e.keys) == 0 {
		return fmt.Sprintf("%s expands to no instances at all.", res.String())
	}
	strs := make([]string, 0, len(e.keys))
	for _, k := range e.keys {
		strs = append(strs, res.Instance(k).String())
	}
	return fmt.Sprintf("%s expands to: %s.", res.String(), strings.Join(strs, ", "))
}

func (e *expansion) scope(key addrs.InstanceKey) instScope {
	sc := instScope{key: key, eachParent: e.eachParent}
	switch {
	case e.counted:
		idx, ok := key.(addrs.IntKey)
		if !ok {
			return sc
		}
		sc.vars = map[string]cty.Value{
			"count": cty.ObjectVal(map[string]cty.Value{
				"index": cty.NumberIntVal(int64(idx)),
			}),
		}
	case e.eachValues != nil:
		sc.vars = map[string]cty.Value{
			"each": cty.ObjectVal(map[string]cty.Value{
				"key":   keyValue(key),
				"value": e.eachValues[key],
			}),
		}
	case e.eachParent != nil:
		// each.value is symbolic here, so only each.key has a value; a
		// reference to each.value is handled structurally instead.
		sc.vars = map[string]cty.Value{
			"each": cty.ObjectVal(map[string]cty.Value{
				"key": keyValue(key),
			}),
		}
	}
	return sc
}

// instScope is the per-instance evaluation scope: the instance key, the
// repetition values that are known, and the parent resource that each.value
// stands for when it is not known.
type instScope struct {
	key        addrs.InstanceKey
	vars       map[string]cty.Value
	eachParent *addrs.Resource
}

func (r *resolver) expansionFor(rc *configs.Resource) (*expansion, bool) {
	key := rc.Addr().String()
	if exp, ok := r.expansions[key]; ok {
		return exp, true
	}
	if r.expFailed[key] {
		return nil, false
	}
	if r.expVisit[key] {
		r.errorf(rc.DeclRange, "Circular for_each reference",
			"The instances of %s depend on themselves, directly or through other resources' for_each expressions.", key)
		r.expFailed[key] = true
		return nil, false
	}
	r.expVisit[key] = true
	defer delete(r.expVisit, key)

	exp, ok := r.buildExpansion(rc)
	if !ok {
		r.expFailed[key] = true
		return nil, false
	}
	r.expansions[key] = exp
	return exp, true
}

func (r *resolver) buildExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()

	switch {
	case rc.Count != nil:
		ident := r.moduleIdentifier(addr.String()+" count", rc.Count.Range())
		val, ok := r.evalStatic(rc.Count, instScope{}, ident)
		if !ok {
			return nil, false
		}
		if !val.IsKnown() || val.IsNull() {
			r.errorf(rc.Count.Range(), "Non-static count expression",
				"The count for %s cannot be determined from configuration alone. A count that depends on a resource attribute cannot be expanded before the cloud is read, and guessing a cardinality would silently drop or invent instances.", addr.String())
			return nil, false
		}
		num, err := convert.Convert(val, cty.Number)
		if err != nil {
			r.errorf(rc.Count.Range(), "Invalid count",
				"The count for %s is not a number: %s.", addr.String(), err)
			return nil, false
		}
		var n int
		if err := gocty.FromCtyValue(num, &n); err != nil {
			r.errorf(rc.Count.Range(), "Invalid count",
				"The count for %s is not a whole number: %s.", addr.String(), err)
			return nil, false
		}
		if n < 0 {
			r.errorf(rc.Count.Range(), "Invalid count",
				"The count for %s is negative.", addr.String())
			return nil, false
		}
		exp := &expansion{counted: true}
		for i := 0; i < n; i++ {
			exp.keys = append(exp.keys, addrs.IntKey(i))
		}
		return exp, true

	case rc.ForEach != nil:
		return r.forEachExpansion(rc)

	case rc.Enabled != nil:
		ident := r.moduleIdentifier(addr.String()+" lifecycle.enabled", rc.Enabled.Range())
		val, ok := r.evalStatic(rc.Enabled, instScope{}, ident)
		if !ok {
			return nil, false
		}
		b, err := convert.Convert(val, cty.Bool)
		if err != nil || !b.IsKnown() || b.IsNull() {
			r.errorf(rc.Enabled.Range(), "Non-static lifecycle.enabled expression",
				"Whether %s exists cannot be determined from configuration alone, so its instances cannot be enumerated.", addr.String())
			return nil, false
		}
		if b.False() {
			return &expansion{}, true
		}
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true

	default:
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true
	}
}

func (r *resolver) forEachExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	// for_each over another resource: the keys are that resource's keys,
	// which is config data even though the values are not.
	if r.isSymbolic(expr, instScope{}) {
		return r.forEachOverResource(rc)
	}

	ident := r.moduleIdentifier(addr.String()+" for_each", expr.Range())
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
		return nil, false
	}
	if !val.IsWhollyKnown() || val.IsNull() {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s cannot be determined from configuration alone. Instance keys are the addresses a projection binds against, so they must be knowable before anything is read from the cloud.", addr.String())
		return nil, false
	}
	if val.IsMarked() {
		r.errorf(expr.Range(), "Sensitive for_each expression",
			"The for_each value for %s is sensitive, so it cannot become part of resource addresses.", addr.String())
		return nil, false
	}

	ty := val.Type()
	exp := &expansion{eachValues: make(map[addrs.InstanceKey]cty.Value)}
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			name := k.AsString()
			names = append(names, name)
			elems[name] = v
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = elems[name]
		}
		return r.checkedForEachKeys(rc, exp)

	case ty.IsSetType():
		if ty.ElementType() != cty.String {
			r.errorf(expr.Range(), "Invalid for_each set",
				"The for_each value for %s is a set of %s. Only a set of strings can produce instance keys.", addr.String(), ty.ElementType().FriendlyName())
			return nil, false
		}
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			names = append(names, v.AsString())
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = cty.StringVal(name)
		}
		return r.checkedForEachKeys(rc, exp)

	default:
		r.errorf(expr.Range(), "Invalid for_each value",
			"The for_each value for %s is %s. for_each accepts a map, an object, or a set of strings.", addr.String(), ty.FriendlyName())
		return nil, false
	}
}

// forEachOverResource handles `for_each = <other resource>`: the fixture's
// aws_route_table_association.this iterating over aws_subnet.this. The keys
// come from the parent's own expansion, so they are knowable even though
// every value in the parent is a live ID.
func (r *resolver) forEachOverResource(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s is computed from another resource's attributes. Only a plain reference to another resource (for_each = aws_subnet.this) can have its instance keys resolved from configuration; anything computed from resource attributes is known only after the cloud is read.", addr.String())
		return nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.diags = r.diags.Append(refDiags)
		return nil, false
	}
	parentAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || parentAddr.Mode != addrs.ManagedResourceMode {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s refers to %s. Instance keys can be propagated only from a whole managed resource (for_each = aws_subnet.this).", addr.String(), ref.Subject.String())
		return nil, false
	}

	parentRC := r.mod.ResourceByAddr(parentAddr)
	if parentRC == nil {
		r.errorf(expr.Range(), "Reference to undeclared resource",
			"The for_each value for %s refers to %s, which is not declared in this configuration.", addr.String(), parentAddr.String())
		return nil, false
	}
	parentExp, ok := r.expansionFor(parentRC)
	if !ok {
		return nil, false
	}
	if parentExp.eachValues == nil && parentExp.eachParent == nil {
		r.errorf(expr.Range(), "for_each over a resource that is not keyed",
			"The for_each value for %s is %s, which does not use for_each, so it is not a map of instances. OpenTofu accepts only a map or a set of strings as a for_each argument.", addr.String(), parentAddr.String())
		return nil, false
	}

	parent := parentAddr
	return &expansion{
		keys:       append([]addrs.InstanceKey(nil), parentExp.keys...),
		eachParent: &parent,
	}, true
}

func (r *resolver) moduleIdentifier(subject string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   subject,
		DeclRange: rng,
	}
}

// ---- small helpers ---------------------------------------------------

func sortedResources(resources map[string]*configs.Resource) []*configs.Resource {
	out := make([]*configs.Resource, 0, len(resources))
	for _, rc := range resources {
		out = append(out, rc)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr().String() < out[j].Addr().String()
	})
	return out
}

func firstPresent(attrs hcl.Attributes, names []string) *hcl.Attribute {
	for _, n := range names {
		if a, ok := attrs[n]; ok {
			return a
		}
	}
	return nil
}

// coalesce merges adjacent literal parts, so that a formula's parts
// alternate literal and parent as far as possible.
func coalesce(parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		if p.Parent == nil && len(out) > 0 && out[len(out)-1].Parent == nil {
			out[len(out)-1].Literal += p.Literal
			continue
		}
		out = append(out, p)
	}
	return out
}

func keyValue(key addrs.InstanceKey) cty.Value {
	switch k := key.(type) {
	case addrs.StringKey:
		return cty.StringVal(string(k))
	case addrs.IntKey:
		return cty.NumberIntVal(int64(k))
	default:
		return cty.NullVal(cty.String)
	}
}

func isAttrStep(step hcl.Traverser, name string) bool {
	attr, ok := step.(hcl.TraverseAttr)
	return ok && attr.Name == name
}

func traversalString(trav hcl.Traversal) string {
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() || ref == nil {
		return trav.RootName()
	}
	return ref.DisplayString()
}

func orList(names []string) string {
	quoted := quoteAll(names)
	switch len(quoted) {
	case 0:
		return "(none)"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}
