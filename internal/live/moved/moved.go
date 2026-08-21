// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package moved carries a configuration's `moved` blocks into the live-marker
// world, where there is no state entry to rewrite.
//
// Under stock OpenTofu a `moved` block relocates a state entry before the
// plan runs, so that the object recorded at the old address is planned as the
// resource declared at the new one. Stateless mode keeps that record on the
// object itself, in its tofu-address tag, so the same statement means
// something slightly different and considerably simpler: *a live resource
// carrying the old address is the object the new address names*. This package
// computes that relation - one declared instance's set of prior addresses -
// and internal/live/discovery indexes a live resource's marker under every
// one of them.
//
// Nothing here writes anything. The marker rewrite that finishes the move is
// the ordinary tags diff every run already computes: once the live resource is
// bound to its new address, the configuration's stamped tofu-address value
// differs from the one on the object, and the provider applies that one tag
// change in place. That is why this package has no apply path and no
// relationship to internal/live/mv, which is the interactive command for the
// case where there is no `moved` block to read.
//
// # Why the relation runs backwards
//
// A `moved` block is written forwards - from the old address to the new one -
// but the question discovery asks is the reverse: given a declared instance,
// which markers could a live resource be carrying and still be this instance?
// [Origins] answers it by applying each statement's endpoints in the opposite
// order through the same [addrs.AbsResourceInstance.MoveDestination] that
// stock's own move execution uses. That reuse is deliberate: module renames,
// root-to-module refactors, cross-module moves and plain resource renames all
// reduce to one structural rewrite there, so none of them needs a case here.
// Chains (a to b, then b to c) are followed transitively, so an estate that
// has not run since two upgrades ago still binds.
//
// # What stays refused, and why it has to
//
// [Honourable] is the single predicate internal/live/lint and
// internal/live/discovery share, and they must not drift: lint refusing a
// shape discovery would have aliased is a needless wall, while lint admitting
// a shape discovery does not alias is the dangerous direction - a live
// resource still carrying the old address reads as an orphan (the plan
// proposes destroying it) while the new address reads as absent (the plan
// proposes creating it). One cloud object, two wrong beliefs. Every refusal
// below exists because the alias cannot be built safely, never because the
// block is untidy.
package moved

import (
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// maxOrigins bounds one declared instance's prior-address set. A chain long
// enough to reach it is not a configuration anyone wrote; the [Origins] walk
// already terminates on its own visited set, and this is only a guard against
// a pathological fan-out of statements all matching one address.
const maxOrigins = 256

// Statement is one `moved` block with both endpoints unified, which is the
// form every address comparison needs: [addrs.UnifyMoveEndpoints] settles
// whether the pair is talking about whole resources, single instances,
// module calls or module instances, so that the two sides can never disagree
// about their own kind.
type Statement struct {
	// From and To are the block's endpoints, relative to Module.
	From, To *addrs.MoveEndpointInModule

	// Module is the static module the block is declared in. A `moved` block
	// in a module applies to every instance of that module, which is what
	// the endpoints' own wildcard module steps express.
	Module addrs.Module

	// DeclRange is the block, for a diagnostic that has to point at it.
	DeclRange hcl.Range
}

// StatementsIn unifies one module's `moved` blocks. A block whose endpoints
// failed to decode is skipped rather than panicked over: the configs package
// has already reported it, and this pass has nothing to add.
//
// This is the whole difference from refactoring.FindMoveStatements, which
// panics on an endpoint pair the decoder rejected because it runs only after
// a configuration has been accepted. Lint runs before that, on configurations
// that may not be accepted at all.
func StatementsIn(mod *configs.Module, path addrs.Module) []Statement {
	if mod == nil {
		return nil
	}
	out := make([]Statement, 0, len(mod.Moved))
	for _, mc := range mod.Moved {
		if mc == nil || mc.From == nil || mc.To == nil {
			continue
		}
		from, to := addrs.UnifyMoveEndpoints(path, mc.From, mc.To)
		if from == nil || to == nil {
			continue
		}
		out = append(out, Statement{
			From:      from,
			To:        to,
			Module:    path,
			DeclRange: mc.DeclRange,
		})
	}
	return out
}

// Statements walks a whole configuration tree and returns every `moved`
// block in it, root first and then children in name order, so that repeated
// runs over the same configuration produce the same list despite the module
// tree living in a Go map.
func Statements(cfg *configs.Config) []Statement {
	return appendStatements(cfg, nil)
}

func appendStatements(cfg *configs.Config, into []Statement) []Statement {
	if cfg == nil || cfg.Module == nil {
		return into
	}
	into = append(into, StatementsIn(cfg.Module, cfg.Path)...)
	for _, name := range childNames(cfg) {
		into = appendStatements(cfg.Children[name], into)
	}
	return into
}

// Honoured is the subset of a configuration's `moved` blocks the live path
// carries through markers: exactly the ones [Honourable] accepts, in
// [Statements] order. It is what internal/live/discovery builds its alias
// index from, and sharing the predicate with lint is what keeps the two from
// disagreeing about which blocks are live.
func Honoured(cfg *configs.Config) []Statement {
	all := Statements(cfg)
	out := make([]Statement, 0, len(all))
	for _, stmt := range all {
		node, ok := configForModule(cfg, stmt.Module)
		if !ok {
			continue
		}
		if _, ok := Honourable(node, stmt); ok {
			out = append(out, stmt)
		}
	}
	return out
}

// Honourable reports whether the live path can carry stmt through ownership
// markers, and when it cannot, the reason in a form a diagnostic Detail can
// use directly. node is the [configs.Config] for stmt.Module - the module the
// block is declared in - because a `moved` block may only ever traverse
// *down* into child modules, so that node reaches both endpoints.
//
// The two refusals are the two ways the alias would be built wrong, not two
// kinds of untidiness:
//
//   - A source address the configuration still declares. The old address is
//     not vacated, so the live resource carrying it stays bound to it and the
//     destination is created fresh - the opposite assignment to the one stock
//     makes, over two objects a later change could tell apart. Stock refuses
//     this too, as "Moved object still exists".
//   - A pair whose two sides name different resource types. A marker names
//     the resource it is written on, so its leading segment is that
//     resource's own type (see internal/live/discovery, markerTypeOf): an
//     alias across types could never match, and the live resource would read
//     as an orphan.
//
// An endpoint passing through a count-expanded module instance is
// deliberately not refused (issue #330, mirroring internal/live/mv's
// checkAddresses fix, issue #317). The premise both refusals once shared -
// "count renumbers every address beneath it" - is the one issue #195 retired
// for a plain scalar module count: shrinking count only ever retires the
// highest index, never renumbers a survivor, so an integer module-instance
// key is exactly as stable an address component as a resource's own
// count-keyed instance. What remains to prove is that the *to* endpoint's
// module, when it is still declared, has a statically evaluable count with no
// count.index leak into its own arguments - and that is [checkChildModules]'
// RuleChildModule, which runs in this very same lint.CheckWith pass (see
// checkConfig in internal/live/lint/lint.go, which calls checkChildModules
// immediately before checkMovedBlocks over every module in the tree) and is
// fatal on its own. So a moved block whose destination module call is unsafe
// never reaches here with a clean lint result regardless of what this
// function does; nothing here needs to re-derive that proof. A *from*
// endpoint typically names a module call the configuration no longer
// declares at all - that is the ordinary shape of a retired address - so
// RuleChildModule has nothing to check there, and needs nothing to check:
// the literal integer index written into the block's own `from` endpoint is
// not an expression subject to a count.index leak, only a stable slot
// address, which #195's fact covers unconditionally.
//
// A count-expanded *resource* is deliberately not refused either. Its
// instances are a fungible set told apart by tofu-slot markers, and
// discovery's set matcher reads claimants off the declared entry an alias
// lands on, never off the marker string - so `count = var.create ? 1 : 0`,
// which is how every terraform-aws-modules resource is written and therefore
// what every `moved` block shipped inside one lands on, carries through
// unchanged.
func Honourable(node *configs.Config, stmt Statement) (string, bool) {
	fromSub, fromOK := subject(stmt, stmt.From)
	toSub, toOK := subject(stmt, stmt.To)
	if !fromOK || !toOK {
		return "its endpoints could not be resolved to addresses", false
	}

	fromType, fromIsResource := resourceTypeOf(fromSub)
	toType, toIsResource := resourceTypeOf(toSub)
	if fromIsResource && toIsResource && fromType != toType {
		return "its two endpoints name different resource types, and an ownership marker names the type of the resource it is written on", false
	}

	if declaresSubject(node, stmt.Module, fromSub) {
		return "the address it moves from is still declared in this configuration, so the move has no vacated address to complete into", false
	}

	return "", true
}

// Origins returns every address a live resource could carry in its
// tofu-address marker and still be the object declared names today, following
// chains of statements transitively. The declared address itself is not
// included: it is what a marker already names when no move is pending.
//
// stmts must already be filtered by [Honourable] - [Honoured] is how a caller
// gets that - because this function performs the structural rewrite without
// re-asking whether it is safe to.
//
// Results are in breadth-first order over stmts, which is deterministic
// because [Statements] is.
func Origins(stmts []Statement, declared addrs.AbsResourceInstance) []addrs.AbsResourceInstance {
	if len(stmts) == 0 {
		return nil
	}
	seen := map[string]bool{declared.String(): true}
	queue := []addrs.AbsResourceInstance{declared}
	var out []addrs.AbsResourceInstance

	for len(queue) > 0 && len(out) < maxOrigins {
		cur := queue[0]
		queue = queue[1:]
		for _, stmt := range stmts {
			// The endpoints are handed over in the opposite order, which
			// turns "where does the object at From end up" into "what could
			// the object now at To have been called". Both sides came out of
			// UnifyMoveEndpoints together, so they are the same object kind
			// in either order and MoveDestination's type assertions hold.
			src, ok := cur.MoveDestination(stmt.To, stmt.From)
			if !ok {
				continue
			}
			key := src.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, src)
			queue = append(queue, src)
		}
	}
	return out
}

// Aliases is [Origins] narrowed to the origins a marker could actually carry:
// the ones naming a resource of declared's own type. A marker names the
// resource it is written on, so its leading segment is that resource's type
// (internal/live/discovery, markerTypeOf); an origin of another type could
// never match a marker on this resource, and honouring it would only widen
// what the caller claims to know. [Honourable] already refuses a statement
// whose two endpoints name different resource types, so this filter fires
// only for a chain that changes type in the middle - two statements each
// type-consistent whose composition is not.
//
// declared itself is not included, exactly as [Origins] does not include it.
func Aliases(stmts []Statement, declared addrs.AbsResourceInstance) []addrs.AbsResourceInstance {
	origins := Origins(stmts, declared)
	if len(origins) == 0 {
		return nil
	}
	typeName := declared.Resource.Resource.Type
	out := make([]addrs.AbsResourceInstance, 0, len(origins))
	for _, origin := range origins {
		if origin.Resource.Resource.Type != typeName {
			continue
		}
		out = append(out, origin)
	}
	return out
}

// Accepts reports whether observed - an escaped tofu-address marker value
// read off a live object, never decoded - names declared: either declared's
// own address, or an address a honoured `moved` block says moved here.
//
// This is the whole definition of "this marker names this instance", and it
// is deliberately the only one. Both sides of the ownership question go
// through it or through [Aliases], which it is defined in terms of:
// internal/live/discovery builds its marker index from [Aliases], and
// internal/live/projection asks this directly for a client-named instance
// whose live object it read by the identity the configuration gave it. Two
// independent implementations would disagree about what an address marker
// means, and that disagreement is exactly what GitHub issue #244 was - one
// layer deferring the check to the other, in a comment, while neither
// performed it.
//
// The comparison is [markers.AddressMatches] rather than a bare equality
// against [markers.EscapeAddress], so a marker a prior run wrote under an
// older escaping grammar still names the instance it always named. A bare
// comparison reintroduces the cross-grammar hole that produced 107 false
// positives earlier in this campaign.
//
// A pending move legitimately leaves the live object carrying the OLD
// address until the marker rewrite lands, which is the ordinary tags diff
// the provider plans (see this package's doc comment). That is why a bare
// equality against declared alone is wrong and would break every `moved`
// block: the aliases are not a courtesy, they are the mechanism.
func Accepts(stmts []Statement, declared addrs.AbsResourceInstance, observed string) bool {
	if observed == "" {
		return false
	}
	if markers.AddressMatches(observed, declared.String()) {
		return true
	}
	for _, alias := range Aliases(stmts, declared) {
		if markers.AddressMatches(observed, alias.String()) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Endpoint inspection
// ---------------------------------------------------------------------------

// subject resolves one endpoint to a concrete address in the unkeyed
// instance of the module the block is declared in. The keys that matter to
// every check here - a count index written into the endpoint itself - are
// carried on the relative part, which this preserves exactly; the module
// prefix is synthesized unkeyed because a `moved` block applies to every
// instance of its own module.
func subject(stmt Statement, e *addrs.MoveEndpointInModule) (addrs.AbsMoveable, bool) {
	if e == nil {
		return nil, false
	}
	inst := make(addrs.ModuleInstance, 0, len(stmt.Module))
	for _, name := range stmt.Module {
		inst = append(inst, addrs.ModuleInstanceStep{Name: name})
	}
	if len(inst) != len(e.Module()) {
		return nil, false
	}
	return e.InModuleInstance(inst), true
}

// resourceTypeOf reports the resource type an endpoint names, and false for a
// module-kind endpoint, which names no single type.
func resourceTypeOf(sub addrs.AbsMoveable) (string, bool) {
	switch s := sub.(type) {
	case addrs.AbsResource:
		return s.Resource.Type, true
	case addrs.AbsResourceInstance:
		return s.Resource.Resource.Type, true
	default:
		return "", false
	}
}

// declaresSubject reports whether the configuration still declares the thing
// an endpoint names: the resource block for a resource-kind endpoint, the
// module call for a module-kind one. node is the config node for declaredIn,
// and the endpoint's address is absolute from the root, so the module prefix
// is dropped before walking down from node.
func declaresSubject(node *configs.Config, declaredIn addrs.Module, sub addrs.AbsMoveable) bool {
	if node == nil || node.Module == nil {
		return false
	}
	switch s := sub.(type) {
	case addrs.AbsResource:
		return declaredResource(node, relSteps(s.Module, declaredIn), s.Resource)
	case addrs.AbsResourceInstance:
		return declaredResource(node, relSteps(s.Module, declaredIn), s.Resource.Resource)
	case addrs.AbsModuleCall:
		rel := relSteps(s.Module, declaredIn)
		child, ok := descend(node, rel)
		if !ok {
			return false
		}
		_, declared := child.Module.ModuleCalls[s.Call.Name]
		return declared
	case addrs.ModuleInstance:
		// A whole module instance: the last step is the call, the rest is
		// the path down to the module that makes it.
		if len(s) == 0 {
			return false
		}
		rel := relSteps(s[:len(s)-1], declaredIn)
		child, ok := descend(node, rel)
		if !ok {
			return false
		}
		_, declared := child.Module.ModuleCalls[s[len(s)-1].Name]
		return declared
	default:
		return false
	}
}

func declaredResource(node *configs.Config, rel addrs.ModuleInstance, res addrs.Resource) bool {
	child, ok := descend(node, rel)
	if !ok {
		return false
	}
	switch res.Mode {
	case addrs.ManagedResourceMode:
		_, declared := child.Module.ManagedResources[res.String()]
		return declared
	case addrs.DataResourceMode:
		_, declared := child.Module.DataResources[res.String()]
		return declared
	default:
		return false
	}
}

// relSteps drops declaredIn's prefix from an absolute module instance path,
// leaving the steps below the node a `moved` block was declared in. A path
// that does not start with that prefix is returned untouched, which cannot
// happen for an endpoint built by [subject] and would only ever make
// declaresSubject answer "not declared".
func relSteps(inst addrs.ModuleInstance, declaredIn addrs.Module) addrs.ModuleInstance {
	if len(inst) < len(declaredIn) {
		return inst
	}
	for i, name := range declaredIn {
		if inst[i].Name != name {
			return inst
		}
	}
	return inst[len(declaredIn):]
}

// descend walks from a config node down the given module steps, by call
// name. A step naming a call this tree does not have stops the walk.
func descend(node *configs.Config, rel addrs.ModuleInstance) (*configs.Config, bool) {
	for _, step := range rel {
		if node == nil {
			return nil, false
		}
		child, ok := node.Children[step.Name]
		if !ok || child == nil || child.Module == nil {
			return nil, false
		}
		node = child
	}
	if node == nil || node.Module == nil {
		return nil, false
	}
	return node, true
}

// configForModule finds the node for a static module path, which is how
// [Honoured] gets the node [Honourable] needs for a block declared in a child
// module.
func configForModule(root *configs.Config, path addrs.Module) (*configs.Config, bool) {
	node := root
	for _, name := range path {
		if node == nil {
			return nil, false
		}
		child, ok := node.Children[name]
		if !ok || child == nil || child.Module == nil {
			return nil, false
		}
		node = child
	}
	if node == nil || node.Module == nil {
		return nil, false
	}
	return node, true
}

func childNames(cfg *configs.Config) []string {
	names := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
