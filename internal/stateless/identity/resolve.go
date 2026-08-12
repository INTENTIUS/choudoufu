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

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/lang"
	"github.com/opentofu/opentofu/internal/tfdiags"
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

	r := &resolver{
		ctx: ctx,
		mod: cfg.Module,
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
	}

	result := newResult()
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

type resolver struct {
	ctx   context.Context
	mod   *configs.Module
	eval  *configs.StaticEvaluator
	diags tfdiags.Diagnostics

	// Expansion memo, keyed by resource address (no instance key).
	expansions map[string]*expansion
	expFailed  map[string]bool
	expVisit   map[string]bool

	// Instance resolution memo, keyed by absolute instance address.
	insts      map[string]Resolution
	instFailed map[string]bool
	instVisit  map[string]bool
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

	entry, ok := LookupType(resAddr.Type)
	if !ok {
		r.errorf(rng, "Resource type outside the live-markers subset",
			"There is no identity knowledge for resource type %q, so %s cannot be admitted to a live-markers projection. "+
				"The v0 identity table covers: %s. See the roadmap's \"The admission rule\".",
			resAddr.Type, addr.String(), strings.Join(AdmittedTypes(), ", "))
		return Resolution{}, false
	}

	if entry.ServerAssigned {
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: entry.Reason,
		}, true
	}

	attrs, ok := r.identityArgs(rc, entry)
	if !ok {
		return Resolution{}, false
	}

	scope := exp.scope(addr.Resource.Key)
	var parts []Part
	for _, comp := range entry.Components {
		if len(comp.Attrs) == 0 {
			parts = append(parts, Part{Literal: comp.Literal})
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
	}

	parts = coalesce(parts)
	return classify(addr, parts), true
}

// classify turns a resolved part list into a Resolution: concrete if every
// part is a literal, parent-derived if any part waits on a live value.
func classify(addr addrs.AbsResourceInstance, parts []Part) Resolution {
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
		return Resolution{
			Addr:     addr,
			Class:    ClassConcrete,
			ImportID: buf.String(),
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
			Parents: parents,
		},
	}
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

	entry, _ := LookupType(parent.Resource.Resource.Type)
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
		r.diags = r.diags.Append(refDiags)
		return cty.NilVal, false
	}

	hclCtx, ctxDiags := r.eval.EvalContext(r.ctx, ident, refs)
	if ctxDiags.HasErrors() {
		r.diags = r.diags.Append(ctxDiags)
		return cty.NilVal, false
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
		r.diags = r.diags.Append(valDiags)
		return cty.NilVal, false
	}
	return val, true
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
