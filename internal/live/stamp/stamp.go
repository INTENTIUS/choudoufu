// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The three marker keys this package writes. They are the discovery
// package's constants rather than new ones: the tags a plan stamps and the
// tags discovery reads back have to be the same strings, and one definition
// is how that stays true.
const (
	TagEstate  = discovery.TagEstate
	TagAddress = discovery.TagAddress
	TagSlot    = discovery.TagSlot
)

// Schemas is how this package learns which resource types are taggable. It is
// the subset of [github.com/intentius/choudoufu/internal/tofu.Schemas] that
// answers that question, so the command hands its schemas over directly and a
// test hands over whatever it likes.
type Schemas interface {
	ResourceTypeConfig(provider addrs.Provider, resourceMode addrs.ResourceMode, resourceType string) (*providers.Schema, uint64)
}

// Request is one stamping pass over one configuration.
type Request struct {
	// Estate is the tofu-estate value to stamp, and must satisfy the marker
	// spec's grammar. There is no "derive it from the configuration" mode
	// here: the caller establishes the estate name (from -estate or from what
	// the configuration already declares) and this package writes what it is
	// told, so that the estate a run discovers with and the estate it stamps
	// with can never drift apart.
	Estate string

	// Config is the configuration to stamp, in place. The whole static
	// module tree is walked: a resource inside a static module call is
	// stamped exactly as a root resource is, with a module-qualified
	// tofu-address. Count- and for_each-expanded module blocks are refused
	// by lint before a run reaches this package, so nothing here has to
	// tell them apart from a static one.
	Config *configs.Config

	// Schemas answers which resource types carry tags.
	Schemas Schemas

	// Slots is the tofu-slot value each count instance carries, keyed by the
	// instance's escaped address - the same string its tofu-address tag
	// holds, e.g. "aws_eip.pool:0". It comes from
	// [discovery.Result.SlotTable]: a slot is minted from the live set of
	// instances, which is discovery's knowledge, so there is no way to
	// compute one from the configuration this package is rewriting.
	//
	// A slot is never a template over count.index. The index is a lookup key
	// into this table and nothing more, which is the difference between "the
	// tool remembers which member of the set this is" and "the tool calls
	// the second one 2" - the latter being exactly the positional identity
	// live/MARKERS.md says a slot must not be. A count block with no
	// entry here gets no tofu-slot tag at all.
	Slots map[string]string

	// NeedsDiscovery names the resource blocks whose instances can only be
	// found by their ownership marker - the identity package's
	// ClassNeedsDiscovery - keyed by module-qualified block address
	// ("aws_subnet.this", or "module.net.aws_subnet.this" inside a static
	// module).
	//
	// It is what turns a failure to stamp from a warning into an error. For a
	// resource whose identity is in its own configuration, an unstamped apply
	// costs the estate its ownership record and nothing else: the next run
	// still finds the resource, by name. For a resource whose identity the
	// provider assigns, the marker is the only handle that will ever exist,
	// so applying it unmarked creates something no later run can see - and
	// every later plan proposes creating another one, forever (audit finding
	// C2). The caller knows which instances are which; this package will not
	// guess it from type names.
	NeedsDiscovery map[string]bool

	// PolicyUntag names the resource blocks GitHub issue #67's
	// declared_tagged = "untag" verb governs, keyed by block address
	// ("aws_subnet.this"), each mapped to the policy tag key that verb
	// releases - [policy.Policy.TagKey], almost always TagEstate.
	//
	// A block named here is stamped exactly as any other except for that
	// one key: instead of asserting it, this pass leaves it out of what it
	// writes, so the object's desired tags lack it and the plan renders an
	// ordinary "~ tags" update removing it (or the key is simply never
	// added, when nothing else needed adding either - see [Result.Untagged]
	// for what actually happened). The caller decides which blocks this
	// governs - the declared_tagged quadrant is worked out from the live
	// object's tags, which this package never reads - the same division of
	// labor [Request.NeedsDiscovery] already has.
	//
	// Granularity is the resource block, not the instance: one
	// configuration body serves every instance of a count or for_each
	// block, so a block named here has the key released for every instance
	// it expands to. A caller that found the key present on only SOME
	// instances' live objects governs the whole block anyway, which is a
	// documented coarsening rather than a silent one - see
	// internal/command's PolicyUntagBlocks.
	PolicyUntag map[string]string
}

// Result is what one pass did, for callers that want to report it. Every
// field is ordered by resource address, so two runs over one configuration
// produce identical output.
type Result struct {
	// Estate is the estate the pass stamped for.
	Estate string

	// Stamped lists the resources whose configuration gained a marker.
	Stamped []Stamped

	// Skipped lists the resources the pass did not stamp, with a reason
	// each. An untaggable type is in here too: "nothing to do" and "could
	// not" are different answers and both are worth being able to see.
	Skipped []Skip

	// Untagged lists the resources GitHub issue #67's declared_tagged =
	// "untag" verb released a tag key from, per [Request.PolicyUntag]. Each
	// resource's other markers are stamped exactly as they would be
	// otherwise; only the named key is left out.
	Untagged []Untagged
}

// Untagged is one resource block whose configuration this pass left one tag
// key out of, at policy's request.
type Untagged struct {
	// Addr is the resource block's address, module-qualified for a block
	// inside a static module - the same shape [Stamped.Addr] and
	// [Skip.Addr] carry.
	Addr addrs.ConfigResource

	// Key is the tag key that was released.
	Key string

	// EstateMarker is true when Key is the tofu-estate marker itself - the
	// case GitHub issue #67 says the plan "must say so in so many words":
	// releasing this key is not a cosmetic tag change, it is this resource
	// leaving management, because the next run's marker discovery can no
	// longer find it by the marker that named it.
	EstateMarker bool
}

// String renders an untag outcome on one line.
func (u Untagged) String() string {
	s := u.Addr.String() + " UNTAG " + u.Key
	if u.EstateMarker {
		s += " (leaves management)"
	}
	return s
}

// Stamped is one resource whose configuration this pass rewrote.
type Stamped struct {
	// Addr is the resource block's address, module-qualified for a block
	// inside a static module.
	Addr addrs.ConfigResource

	// Keys are the marker keys that were added, in the order they appear in
	// MARKERS.md.
	Keys []string

	// Address is the tofu-address value written. For a resource with count or
	// for_each the value is per instance, and this field carries the escaped
	// prefix with the instance key's source named - "aws_eip.pool:count.index"
	// - because there is no single value to report.
	Address string

	// PerInstance is true when Address is a template over count.index or
	// each.key rather than a constant.
	PerInstance bool

	// Merged is true when the markers were injected into a tags expression
	// this pass could not read entry by entry - a merge() call, a variable, a
	// conditional - rather than written into an object literal. The markers
	// this run stamped are the ones that land either way, because they go
	// last and merge's last argument wins.
	Merged bool
}

// SkipReason is why one resource was not stamped.
type SkipReason string

const (
	// SkipUntaggable is a resource type with no tags argument in its schema.
	// Not a problem: those types are identified by other means entirely.
	SkipUntaggable SkipReason = "UNTAGGABLE"

	// SkipNoSchema is a resource type whose schema this pass could not read.
	// The plan that follows will fail on it for its own reasons.
	SkipNoSchema SkipReason = "NO_SCHEMA"

	// SkipAlreadyStamped is a resource that already declares both markers,
	// correctly. The no-op case, recorded rather than silent.
	SkipAlreadyStamped SkipReason = "ALREADY_STAMPED"

	// SkipTagsUnreadable is a tags argument this pass can neither read entry
	// by entry nor merge into: one that evaluates to something which is not a
	// collection at all. An expression it merely cannot read - a merge()
	// call, a variable, a conditional - is no longer one of these; the markers
	// are merged into it.
	SkipTagsUnreadable SkipReason = "TAGS_UNREADABLE"

	// SkipMarkerUnreadable is a marker key that is present but whose value
	// does not evaluate from configuration alone, so it can be neither
	// verified nor (this package never overwrites) replaced.
	SkipMarkerUnreadable SkipReason = "MARKER_UNREADABLE"

	// SkipNotHCL is a resource written in JSON syntax, whose body this
	// package cannot rewrite.
	SkipNotHCL SkipReason = "NOT_HCL_SYNTAX"

	// SkipUntagHandWritten is a resource GitHub issue #67's declared_tagged
	// = "untag" verb governs, whose configuration already hardcodes the
	// key that verb would release. This pass never overwrites a
	// hand-written tag value anywhere, and untag is not an exception - see
	// [Request.PolicyUntag].
	SkipUntagHandWritten SkipReason = "UNTAG_HAND_WRITTEN"

	// SkipModuleKeyed is a resource declared inside a module call that sets
	// for_each (59c, issue #59 phase 3), directly or through an ancestor.
	// This pass neither writes nor verifies its markers: the module's
	// several instances share one HCL body for the resource's tags
	// argument, and there is no way to inject a different literal
	// tofu-address into it per instance, or to safely re-evaluate an
	// existing one that may depend on a variable the module call threads
	// through from its own each.key. See [stamper.moduleKeyedResource].
	SkipModuleKeyed SkipReason = "MODULE_KEYED"

	// SkipModuleKeyedTrusted is the benign half of the case above: the
	// resource is inside a for_each'd module AND already declares a tags
	// argument, so its markers are the operator's own hand-written ones and
	// this pass leaves them alone. Nothing is missing and nothing is wrong.
	//
	// It is a separate reason because a Skip carries no severity of its own
	// and its consumer has to infer one. statelessStampGaps turns any
	// unexempted skip on a needs-discovery resource into a hard error, so
	// while both halves of moduleKeyedResource shared MODULE_KEYED, the
	// hand-stamped idiom live/LIMITATIONS.md documents was indistinguishable
	// from an unmarked resource about to be created unfindable. That went
	// unnoticed only because a key-form bug (#111) kept statelessStampGaps
	// inert inside keyed modules; fixing the key made it fire on the wrong
	// half. Keep the two reasons distinct.
	SkipModuleKeyedTrusted SkipReason = "MODULE_KEYED_TRUSTED"
)

// Skip is one resource this pass left alone, and why.
type Skip struct {
	// Addr is the resource block's address, module-qualified for a block
	// inside a static module.
	Addr   addrs.ConfigResource
	Reason SkipReason

	// Detail is one sentence aimed at an operator. Empty for the reasons
	// that need none (an untaggable type, a resource already stamped).
	Detail string
}

// String renders a skip on one line, for logs and test failures.
func (s Skip) String() string {
	if s.Detail == "" {
		return s.Addr.String() + " " + string(s.Reason)
	}
	return s.Addr.String() + " " + string(s.Reason) + ": " + s.Detail
}

// Stamp injects the estate's ownership markers into every taggable managed
// resource in the configuration that does not already declare them, by
// rewriting the resource bodies in place. See the package doc for why the
// injection happens here, in configuration, rather than in the plan's
// recorded changes.
//
// Nothing is written until the whole configuration has been checked. A
// conflicting marker (a different estate, or a different address) makes the
// pass return errors and touch nothing, because a run that stops on a
// conflict should leave the configuration exactly as it found it - the
// operator's next move is to read the resource that conflicts, and it should
// read the way they wrote it.
func Stamp(ctx context.Context, req Request) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	res := &Result{Estate: req.Estate}

	switch {
	case !discovery.ValidEstateName(req.Estate):
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No estate name to stamp with",
			fmt.Sprintf("Stamping ownership markers needs the estate's name, matching the tofu-estate grammar in live/MARKERS.md (a lowercase letter followed by letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.Config == nil || req.Config.Module == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to stamp",
			"Stamping ownership markers needs the configuration this run planned from, and none was given.",
		))
	case req.Schemas == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider schemas for marker stamping",
			"Which resource types can carry a marker is read from the provider schemas, and none were given. This is a bug.",
		))
	}

	s := &stamper{req: req, res: res}

	var pending []*rewrite
	for _, mr := range moduleResources(req.Config) {
		rw, resDiags := s.resource(ctx, mr.rc, mr.mod, mr.modInst, mr.keyedAncestor)
		diags = diags.Append(resDiags)
		if rw != nil {
			pending = append(pending, rw)
		}
	}

	if diags.HasErrors() {
		// A conflict anywhere means nothing is rewritten anywhere. See the
		// function doc.
		res.Stamped = nil
		return res, diags
	}
	for _, rw := range pending {
		rw.apply()
	}
	return res, diags
}

// moduleResource is one managed resource block together with the module it
// is declared in, both forms: the *configs.Module for reading its own static
// evaluator, and the module's instance path for module-qualifying its
// address.
type moduleResource struct {
	rc      *configs.Resource
	mod     *configs.Module
	modInst addrs.ModuleInstance

	// keyedAncestor is true when rc is declared inside a module call that
	// sets for_each - directly, or through any ancestor module call - at
	// any depth (59c, issue #59 phase 3). It is what tells [stamper.resource]
	// to take the "cannot compute a per-instance marker" path instead of the
	// ordinary one.
	//
	// modInst deliberately stays the *unkeyed* self instance
	// ([identity.ModuleInstance]) even for such a resource, one visit per rc
	// rather than one per instance: unlike the five walkers that read the
	// live system, this package rewrites the configuration file's own text,
	// and a for_each'd module's several instances share exactly one
	// *hclsyntax.Body for their tags argument. There is no way to inject two
	// different literal tofu-address values into one shared body - only an
	// expression that varies per real module instance could, and building
	// one would mean rewriting the module call block to pass a variable
	// through from its own each.key, which is configuration surgery well
	// beyond a tags argument and is not something this pass attempts. See
	// [stamper.moduleKeyedResource].
	keyedAncestor bool
}

// moduleResources walks the whole static module tree in deterministic order
// - one module's resources, sorted by name, then its children in name order
// - so that two runs over one configuration stamp in the same order and
// [Result.Stamped] comes out sorted exactly as it always has for a
// root-only configuration.
//
// The walk visits each module CALL once, not once per instance: whether a
// for_each'd module's calls fan out to one instance or a hundred makes no
// difference to what this pass can do with their shared configuration text
// (see [moduleResource.keyedAncestor]), so there is nothing to gain and a
// great deal to lose - a spurious marker-conflict diagnostic, or worse, a
// rewrite applied twice - by visiting the same *configs.Resource more than
// once.
func moduleResources(cfg *configs.Config) []moduleResource {
	return moduleResourcesFrom(cfg, false)
}

func moduleResourcesFrom(cfg *configs.Config, keyedAncestor bool) []moduleResource {
	if cfg == nil || cfg.Module == nil {
		return nil
	}
	modInst := identity.ModuleInstance(cfg)
	mod := cfg.Module

	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]moduleResource, 0, len(names))
	for _, name := range names {
		out = append(out, moduleResource{rc: mod.ManagedResources[name], mod: mod, modInst: modInst, keyedAncestor: keyedAncestor})
	}
	for _, name := range identity.SortedChildNames(cfg.Children) {
		childKeyed := keyedAncestor
		if call, ok := mod.ModuleCalls[name]; ok && call != nil && call.ForEach != nil {
			childKeyed = true
		}
		out = append(out, moduleResourcesFrom(cfg.Children[name], childKeyed)...)
	}
	return out
}

// stamper carries one pass's inputs and accumulates its result.
type stamper struct {
	req Request
	res *Result

	// mod and modInst are the module currently being stamped: set at the
	// top of [stamper.resource] for each resource in turn, since resources
	// from different modules in the walk need different static evaluators
	// and different address prefixes.
	mod     *configs.Module
	modInst addrs.ModuleInstance
}

// rewrite is one resource's decided mutation, held until the whole
// configuration has been checked.
//
// The four shapes are the four ways a tags argument can be written, and every
// one of them can carry a marker. Only the first two existed before: a bare
// resource gained a tags argument, an object literal gained two entries, and
// everything else - merge(), a variable, a conditional - was skipped with a
// warning, which for a marker-discovered type meant an apply that created a
// resource nothing could ever find again (audit finding C2). Injection into an
// expression this pass cannot read entry by entry is the answer, and merge is
// what makes it sound: the marker object goes last, and merge's last argument
// wins, so whatever the unreadable half produces the markers on the applied
// resource are the ones this run stamped. The plan renders the resulting tags
// like any other, so nothing about it is invisible.
type rewrite struct {
	// body is the resource's own configuration body, which gains a tags
	// argument when every other field is nil.
	body *hclsyntax.Body

	// obj is the existing tags object literal to append to.
	obj *hclsyntax.ObjectConsExpr

	// merge is an existing merge() call the marker object is appended to as a
	// final argument.
	merge *hclsyntax.FunctionCallExpr

	// wrap is a tags argument whose expression this pass cannot read, which
	// is replaced by merge(<the expression>, {markers}).
	wrap *hclsyntax.Attribute

	// replace is a tags argument that evaluates to null, whose expression is
	// replaced by the marker object outright: merging into null is an error,
	// and "tags = null" says there are no tags rather than that there is
	// something to preserve.
	replace bool

	// items are the marker entries to add.
	items []hclsyntax.ObjectConsItem

	// rng is the range synthesized nodes point at: the resource's own
	// declaration, so that a diagnostic about an injected value lands on the
	// resource it was injected into.
	rng hcl.Range
}

func (rw *rewrite) apply() {
	switch {
	case rw.obj != nil:
		rw.obj.Items = append(rw.obj.Items, rw.items...)
	case rw.merge != nil:
		rw.merge.Args = append(rw.merge.Args, rw.markerObject())
	case rw.wrap != nil && rw.replace:
		rw.wrap.Expr = rw.markerObject()
	case rw.wrap != nil:
		rw.wrap.Expr = &hclsyntax.FunctionCallExpr{
			Name:            "merge",
			Args:            []hclsyntax.Expression{rw.wrap.Expr, rw.markerObject()},
			NameRange:       rw.rng,
			OpenParenRange:  rw.rng,
			CloseParenRange: rw.rng,
		}
	default:
		if rw.body.Attributes == nil {
			rw.body.Attributes = hclsyntax.Attributes{}
		}
		rw.body.Attributes["tags"] = &hclsyntax.Attribute{
			Name:        "tags",
			Expr:        rw.markerObject(),
			SrcRange:    rw.rng,
			NameRange:   rw.rng,
			EqualsRange: rw.rng,
		}
	}
}

func (rw *rewrite) markerObject() *hclsyntax.ObjectConsExpr {
	return &hclsyntax.ObjectConsExpr{Items: rw.items, SrcRange: rw.rng, OpenRange: rw.rng}
}

// resource decides what one resource block needs, without writing anything.
func (s *stamper) resource(ctx context.Context, rc *configs.Resource, mod *configs.Module, modInst addrs.ModuleInstance, keyedAncestor bool) (*rewrite, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	s.mod = mod
	s.modInst = modInst
	addr := addrs.ConfigResource{Module: modInst.Module(), Resource: rc.Addr()}

	schema, _ := s.req.Schemas.ResourceTypeConfig(rc.Provider, rc.Mode, rc.Type)
	if schema == nil || schema.Block == nil {
		detail := fmt.Sprintf("The schema of %s is not available, so whether %s can carry a marker is unknown and no marker was written.", rc.Type, addr)
		s.skip(addr, SkipNoSchema, detail)
		if !s.mustStamp(rc) {
			// The plan that follows will fail on this resource for its own
			// reasons, and with a better message than this pass could give.
			return nil, diags
		}
		return nil, diags.Append(s.unstampable(rc, detail))
	}
	if !taggable(schema.Block) {
		// Silent for a resource identified some other way: an untaggable type
		// is not a gap in the estate's records, it is a type whose identity
		// comes from somewhere else. Loud for one that has no other way to be
		// found, where it is the estate's records and the resource both.
		s.skip(addr, SkipUntaggable, "")
		if !s.mustStamp(rc) {
			return nil, diags
		}
		return nil, diags.Append(s.unstampable(rc, fmt.Sprintf(
			"%s is a %s, whose schema has no tags map this configuration can set, so there is nowhere to carry an ownership marker.",
			addr, rc.Type)))
	}
	if keyedAncestor {
		return s.moduleKeyedResource(rc, addr)
	}

	body, ok := rc.Config.(*hclsyntax.Body)
	if !ok {
		detail := fmt.Sprintf(
			"%s is written in JSON syntax, whose body this run cannot rewrite, so its %s and %s tags were not injected. Write the two marker tags into its tags argument by hand, or convert the file to HCL native syntax.",
			addr, TagEstate, TagAddress)
		s.skip(addr, SkipNotHCL, "Its configuration is not in HCL native syntax.")
		return nil, diags.Append(s.unstampable(rc, detail))
	}

	rw, existing, static, writable := s.tagsWrite(ctx, rc, body)
	if !writable {
		detail := fmt.Sprintf(
			"%s sets tags with an expression this run can neither read entry by entry nor merge into, so the %s and %s markers were not injected. Write the two marker tags in an object literal, or add them to whatever the expression produces.",
			addr, TagEstate, TagAddress)
		s.skip(addr, SkipTagsUnreadable, "Its tags argument is neither readable nor mergeable.")
		return nil, diags.Append(s.unstampable(rc, detail))
	}

	wantAddr, addrDisplay, perInstance := addressExpr(rc, modInst)
	markers := []marker{
		{key: TagEstate, expr: &hclsyntax.LiteralValueExpr{Val: cty.StringVal(s.req.Estate), SrcRange: rc.DeclRange}, want: s.req.Estate},
	}
	markers = append(markers, s.splitAddressMarker(ctx, rc, modInst, wantAddr, addrDisplay, perInstance)...)
	if slotExpr, slotDisplay, ok := s.slotExpr(rc); ok {
		markers = append(markers, marker{key: TagSlot, expr: slotExpr, want: slotDisplay, perInstance: true})
	}

	untagKey := s.req.PolicyUntag[addr.String()]

	var added []string
	var untagged []string
	for _, m := range markers {
		if untagKey != "" && m.key == untagKey {
			cur, present := existing[m.key]
			_, knownValue := static[m.key]
			if !present && !knownValue {
				// The common case: the configuration does not already
				// hardcode this key, so simply not adding it is exactly
				// what "released" means - the desired tags lack it, and
				// the plan renders the removal as an ordinary "~ tags"
				// update (or, when nothing else about this resource needed
				// stamping either, there is nothing to rewrite at all; see
				// the len(added)==0 handling below).
				untagged = append(untagged, m.key)
				continue
			}
			// A hand-written value for the key this run was asked to
			// release. This pass never overwrites a hand-written marker
			// value anywhere else, and untag is not an exception: the
			// author wrote it, so it stays, and the release does not
			// happen this run.
			_ = cur
			s.skip(addr, SkipUntagHandWritten, fmt.Sprintf(
				"%s sets %q directly in its tags argument, so declared_tagged = \"untag\" did not release it: this pass never overwrites a hand-written tag value. Remove it from the configuration to release it.",
				addr, m.key))
			continue
		}
		cur, present := existing[m.key]
		got, knownValue := static[m.key]
		switch {
		case !present && !knownValue:
			rw.items = append(rw.items, markerItems(m, rc.DeclRange)...)
			added = append(added, m.key)
			continue
		case !present:
			// The marker is in a tags expression whose value this run could
			// evaluate but whose source it cannot append to - a local, a
			// variable, a merge() argument that is one of those. There is a
			// value to check, and no expression to point a diagnostic at.
			switch verdict, detail := s.verifyValue(rc, m, got); verdict {
			case verifyOK:
			case verifyConflict:
				diags = diags.Append(&hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  SummaryMarkerConflict,
					Detail:   detail,
					Subject:  rc.DeclRange.Ptr(),
				})
			}
			continue
		}
		switch verdict, detail := s.verify(ctx, rc, m, cur); verdict {
		case verifyOK:
			// The author already wrote it, and wrote it right.
		case verifyConflict:
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  SummaryMarkerConflict,
				Detail:   detail,
				Subject:  cur.Range().Ptr(),
			})
		case verifyUnreadable:
			s.skip(addr, SkipMarkerUnreadable, detail)
			diags = diags.Append(s.unstampableAt(rc, cur.Range(), SummaryMarkerUncheckable, detail))
		}
	}

	for _, k := range untagged {
		s.res.Untagged = append(s.res.Untagged, Untagged{Addr: addr, Key: k, EstateMarker: k == TagEstate})
	}

	if len(added) == 0 {
		switch {
		case len(untagged) > 0:
			// Nothing to rewrite: the configuration already looks the way
			// "untag" wants it to (the key was never going to be added),
			// so the release already happened - it just was not a rewrite
			// this pass had to make. Recorded in Untagged above rather
			// than folded into SkipAlreadyStamped, which would say nothing
			// happened when a policy verb did.
		case !diags.HasErrors():
			s.skip(addr, SkipAlreadyStamped, "")
		}
		return nil, diags
	}

	s.res.Stamped = append(s.res.Stamped, Stamped{
		Addr:        addr,
		Keys:        added,
		Address:     addrDisplay,
		PerInstance: perInstance,
		Merged:      rw.merge != nil || rw.wrap != nil,
	})
	return rw, diags
}

// moduleKeyedResource is [stamper.resource]'s whole handling of a taggable
// resource declared inside a for_each'd module (directly, or through an
// ancestor call): see [SkipModuleKeyed] and [moduleResource.keyedAncestor]
// for why neither writing nor verifying a marker is attempted here.
//
// A resource that already declares a tags argument is trusted as written -
// the operator (or a generator such as tools/estate-gen's -module-wrap
// keyed mode) is expected to have built its tofu-address from a variable
// the module call threads through from its own each.key, the ordinary
// OpenTofu idiom for a value that must vary per module instance, and
// checking that expression would mean evaluating var.* through the static
// evaluator with no way to supply the per-instance value it needs - the
// same evaluation a bare reference to the module's own each.key panics on
// (internal/configs' static scope has no repetition data at all; see
// [ChildModuleKeys]'s doc). A resource with no tags argument at all gets
// the ordinary must-stamp severity: a marker-discovered type applying
// unmarked is still the unrecoverable mistake [stamper.unstampable] exists
// to catch, module or not.
func (s *stamper) moduleKeyedResource(rc *configs.Resource, addr addrs.ConfigResource) (*rewrite, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	body, ok := rc.Config.(*hclsyntax.Body)
	hasTags := ok && body.Attributes != nil && body.Attributes["tags"] != nil
	detail := fmt.Sprintf(
		"%s is declared inside a module call that expands with for_each, so its %s and %s markers cannot be computed here: "+
			"the module's instances share one configuration body for the resource's tags argument, and there is no single "+
			"literal address that is right for all of them. Declare tags = { %s = ..., %s = ... } by hand, building the "+
			"address from a variable the module call passes through from its own each.key (the ordinary way a value that "+
			"must vary per module instance reaches a child module's resources) - see live/LIMITATIONS.md, \"child-module\".",
		addr, TagEstate, TagAddress, TagEstate, TagAddress,
	)
	if hasTags {
		// Trusted as written; see the function doc for why this pass cannot
		// safely check it. SkipModuleKeyedTrusted rather than
		// SkipModuleKeyed: this outcome is benign and its consumer needs to
		// tell it apart from the branch below, which is not.
		s.skip(addr, SkipModuleKeyedTrusted, "Declared inside a for_each'd module; its markers are trusted as written, not verified.")
		return nil, diags
	}
	s.skip(addr, SkipModuleKeyed, detail)
	if !s.mustStamp(rc) {
		return nil, diags
	}
	return nil, diags.Append(s.unstampable(rc, detail))
}

// mustStamp reports whether this resource's instances can only ever be found
// by their ownership marker, which is what decides the severity of every
// failure to write one. See [Request.NeedsDiscovery].
func (s *stamper) mustStamp(rc *configs.Resource) bool {
	key := addrs.ConfigResource{Module: s.modInst.Module(), Resource: rc.Addr()}.String()
	return s.req.NeedsDiscovery[key]
}

// unstampable is the diagnostic for a resource that did not get its markers,
// at the severity its identity class earns.
func (s *stamper) unstampable(rc *configs.Resource, detail string) *hcl.Diagnostic {
	return s.unstampableAt(rc, rc.DeclRange, SummaryNotStamped, detail)
}

// unstampableAt is [stamper.unstampable] pointing at a range inside the
// resource rather than at the resource itself.
func (s *stamper) unstampableAt(rc *configs.Resource, rng hcl.Range, summary, detail string) *hcl.Diagnostic {
	if !s.mustStamp(rc) {
		return &hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Summary:  summary,
			Detail:   detail,
			Subject:  rng.Ptr(),
		}
	}
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  SummaryUnmarkedApply,
		Detail:   detail + " " + unmarkedDiscoveryDetail(addrs.ConfigResource{Module: s.modInst.Module(), Resource: rc.Addr()}),
		Subject:  rng.Ptr(),
	}
}

// unmarkedDiscoveryDetail is the sentence that makes an unstamped
// marker-discovered resource an error rather than a warning.
func unmarkedDiscoveryDetail(addr addrs.ConfigResource) string {
	return fmt.Sprintf(
		"%s has an identity the provider assigns at create time, so the ownership marker is the only thing any later run can find it by. Applying it unmarked would create a resource this configuration can never see again, and every later plan would propose creating another one.",
		addr)
}

func (s *stamper) skip(addr addrs.ConfigResource, reason SkipReason, detail string) {
	s.res.Skipped = append(s.res.Skipped, Skip{Addr: addr, Reason: reason, Detail: detail})
}

// marker is one key this pass would write, with the expression that writes it
// and a rendering of the value for messages.
type marker struct {
	key  string
	expr hclsyntax.Expression

	// want is the value in operator-readable form. For a per-instance
	// address it names the instance-key source rather than a value.
	want string

	perInstance bool
}

// markerItems is the object entries one marker contributes.
//
// It returns a slice rather than a single item because of the seam the
// tofu-slot marker needed (see the package doc): a count instance's slot is a
// third entry alongside tofu-address, written from the same place. That entry
// is [slotExpr]'s lookup table, keyed by the same escaped instance address
// this function's callers already build.
func markerItems(m marker, rng hcl.Range) []hclsyntax.ObjectConsItem {
	return []hclsyntax.ObjectConsItem{{
		KeyExpr:   &hclsyntax.ObjectConsKeyExpr{Wrapped: &hclsyntax.LiteralValueExpr{Val: cty.StringVal(m.key), SrcRange: rng}},
		ValueExpr: m.expr,
	}}
}

type verdict int

const (
	verifyOK verdict = iota
	verifyConflict
	verifyUnreadable
)

// verify decides what to do about a marker the configuration already
// declares. It never returns "overwrite": there is no such verdict.
func (s *stamper) verify(ctx context.Context, rc *configs.Resource, m marker, cur hclsyntax.Expression) (verdict, string) {
	addr := rc.Addr()

	// A per-instance address is not a constant, so the check is structural:
	// does the author's expression build the same escaped address out of the
	// same instance key this pass would have used.
	if m.perInstance {
		if sameExpr(cur, m.expr) {
			return verifyOK, ""
		}
		if got, ok := s.staticString(ctx, rc, cur); ok {
			return s.verifyValue(rc, m, got)
		}
		return verifyUnreadable, fmt.Sprintf(
			"%s sets %s to an expression this run cannot read from configuration alone, so it cannot check that it matches what this estate would stamp (%s). The tag was left exactly as written; nothing was overwritten.",
			addr, m.key, m.want)
	}

	got, ok := s.staticString(ctx, rc, cur)
	if !ok {
		return verifyUnreadable, fmt.Sprintf(
			"%s sets %s to an expression this run cannot read from configuration alone, so it cannot check that it is %q. The tag was left exactly as written; nothing was overwritten.",
			addr, m.key, m.want)
	}
	return s.verifyValue(rc, m, got)
}

// verifyValue is [stamper.verify] once the marker's current value is in hand,
// whether it came from the author's own expression or from evaluating a tags
// argument this pass cannot append to. Same rules, same wording, one
// implementation.
func (s *stamper) verifyValue(rc *configs.Resource, m marker, got string) (verdict, string) {
	addr := rc.Addr()

	if m.perInstance {
		if m.key == TagSlot {
			return verifyConflict, fmt.Sprintf(
				"%s has %s, so its instances are a fungible set, but its %s tag is the constant %q and every instance would claim the same slot. Remove the tag and let this run stamp the assignment it worked out from the live set (%s).",
				addr, instanceKeyword(rc), TagSlot, got, m.want)
		}
		// Not "Write it as %q" with m.want: m.want is addressExpr's DISPLAY
		// string, which omits the interpolation - "aws_eip.pool:count.index"
		// rather than "aws_eip.pool:${count.index}", and for a continuation
		// chunk it carries a " (chunk 1 of 2)" suffix that is not HCL at
		// all. A user following that literally writes a constant on every
		// instance, which is the error being reported. See #101.
		return verifyConflict, fmt.Sprintf(
			"%s has %s, so each instance owns a different address, but its %s tag is the constant %q. Remove the tag and let this run stamp the per-instance value, or write it as an expression that interpolates %s.",
			addr, instanceKeyword(rc), m.key, got, instanceRefKeyword(rc))
	}

	if got == m.want {
		return verifyOK, ""
	}

	switch m.key {
	case TagEstate:
		return verifyConflict, fmt.Sprintf(
			"%s declares %s = %q and this run is stamping the estate %q. A plan never overwrites a marker naming another estate: name %s in the live block (or with -estate, if this configuration has no live block) if that is the estate this run is for, or correct the tag.",
			addr, TagEstate, got, m.want, got)
	case TagAddress:
		return verifyConflict, fmt.Sprintf(
			"%s declares %s = %q, but its address in this configuration is %q. A marker naming another address is a rename: run `choudoufu live-mv %s %s`, or fix the tag. See live/MARKERS.md, \"The rename rule\".",
			addr, TagAddress, got, m.want, got, m.want)
	default:
		// A continuation tag (tofu-address-2, tofu-address-3, ...): part of
		// a longer address, not an address by itself, so neither of the two
		// messages above fits it. See live/MARKERS.md, "tofu-address
		// continuation tags".
		return verifyConflict, fmt.Sprintf(
			"%s declares %s = %q, a tofu-address continuation tag, but this run would write a different value there. Continuation tags are written automatically alongside %s and are never edited by hand; remove %s and let this run rewrite the whole set, or run `choudoufu live-mv <old-address> <new-address>` if the resource's address is actually changing. See live/MARKERS.md, \"tofu-address continuation tags\".",
			addr, m.key, got, TagAddress, m.key)
	}
}

// staticString evaluates an expression with the module's static evaluator -
// constants, locals, variables and functions - and reports the string it
// produced. Anything the static evaluator cannot answer (a reference to
// another resource, count.index, each.key) reports not-ok, which is the
// "cannot check" case rather than a failure.
func (s *stamper) staticString(ctx context.Context, rc *configs.Resource, expr hclsyntax.Expression) (string, bool) {
	if val, diags := expr.Value(nil); !diags.HasErrors() && !val.IsNull() && val.IsKnown() && !val.IsMarked() && val.Type() == cty.String {
		// The overwhelmingly common case, and the one that needs no
		// evaluator at all: a quoted constant.
		return val.AsString(), true
	}
	val, ok := s.staticValue(ctx, rc, expr)
	if !ok || val.IsNull() || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}

// staticValue evaluates an expression with the module's static evaluator and
// returns whatever value it produced, of whatever type - a whole tags map
// included. It is [stamper.staticString] without the string requirement, for
// the tags argument this pass evaluates as a whole in order to see the markers
// inside it.
//
// [moduleKeyedResource] keeps this from ever being reached for a resource
// declared inside a for_each'd module, but the recover guards it anyway,
// the same defensive reason [resolver.evalPure] in internal/live/identity
// carries one: internal/configs' static scope panics rather than errors on
// a reference it has no repetition data for, and a future caller of this
// function - or a keyed-module case this pass's own reasoning missed -
// should degrade to "cannot evaluate" rather than crash the run.
func (s *stamper) staticValue(ctx context.Context, rc *configs.Resource, expr hclsyntax.Expression) (val cty.Value, ok bool) {
	defer func() {
		if recover() != nil {
			val, ok = cty.NilVal, false
		}
	}()

	if s.mod == nil || s.mod.StaticEvaluator == nil {
		return cty.NilVal, false
	}
	v, diags := s.mod.StaticEvaluator.Evaluate(ctx, expr, configs.StaticIdentifier{
		Module:    s.modInst.Module(),
		Subject:   fmt.Sprintf("%s.tags", rc.Addr()),
		DeclRange: expr.Range(),
	})
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsMarked() {
		return cty.NilVal, false
	}
	return v, true
}

func instanceKeyword(rc *configs.Resource) string {
	if rc.Count != nil {
		return "count"
	}
	return "for_each"
}

// instanceRefKeyword is the expression a per-instance marker has to
// interpolate, written the way a user would type it. It exists because the
// obvious thing to quote at them - addressExpr's m.want - is a DISPLAY
// string with the "${...}" stripped out, so quoting it hands back a constant
// and reproduces the error being reported (#101).
func instanceRefKeyword(rc *configs.Resource) string {
	if rc.Count != nil {
		return "${count.index}"
	}
	return "${each.key}"
}

// ---------------------------------------------------------------------------
// Reading and building tag expressions
// ---------------------------------------------------------------------------

// taggable reports whether a resource type can carry a marker: a top-level
// "tags" attribute of a map type that configuration is allowed to set.
//
// Read from the schema, never from a list of type names. A list would be
// wrong the day a provider adds tags to a type, and it would be wrong in the
// other direction for every provider nobody thought about. The tags-as-nested-
// blocks shape some types use (an aws_autoscaling_group's tag blocks) is not
// this, and is deliberately not stamped: those blocks are not the tag map the
// marker spec describes.
func taggable(block *configschema.Block) bool {
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return false
	}
	if !attr.Optional && !attr.Required {
		// Computed-only: the provider owns the value and configuration
		// cannot set it.
		return false
	}
	ty := attr.Type
	if !ty.IsMapType() {
		return false
	}
	et := ty.ElementType()
	return et == cty.String || et == cty.DynamicPseudoType
}

// tagsWrite works out where a resource's markers can be written and what its
// tags argument already says.
//
// The four returns are the rewrite with its destination chosen and no items in
// it yet, the marker keys the configuration already sets as expressions this
// pass can point a diagnostic at, the marker values it could evaluate but not
// point at, and whether a marker can be written at all.
//
// A tags argument is one of four things, and only the first two were ever
// handled. A body with no tags argument gains one. An object literal with
// literal keys is appended to, which is where the entries-by-key answer comes
// from. A merge() call gains a final argument, since merge takes any number of
// them and the last one wins - and the literal object arguments it does have
// are read for markers on the way past. Anything else is wrapped in a merge()
// of itself and the marker object, after an attempt to evaluate it statically:
// a tags argument built from locals and variables usually does evaluate, and
// knowing what it produces is the difference between checking a marker and
// overwriting one.
func (s *stamper) tagsWrite(ctx context.Context, rc *configs.Resource, body *hclsyntax.Body) (*rewrite, map[string]hclsyntax.Expression, map[string]string, bool) {
	rw := &rewrite{body: body, rng: rc.DeclRange}

	attr, ok := body.Attributes["tags"]
	if !ok {
		return rw, nil, nil, true
	}

	switch expr := attr.Expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		if entries, complete := literalEntries(expr); complete {
			rw.obj = expr
			return rw, entries, nil, true
		}
		// An object with a key this pass cannot read could already carry a
		// marker under that key, and appending a second entry for the same key
		// is not something HCL forgives. Merging is the safe shape.
	case *hclsyntax.FunctionCallExpr:
		if expr.Name == "merge" {
			rw.merge = expr
			return rw, mergeEntries(expr), nil, true
		}
	}

	rw.wrap = attr
	val, ok := s.staticValue(ctx, rc, attr.Expr)
	if !ok {
		return rw, nil, nil, true
	}
	if val.IsNull() {
		// merge(null, {...}) is an error, and "tags = null" is a statement
		// that there are no tags rather than that there is something to keep.
		rw.replace = true
		return rw, nil, nil, true
	}
	if !val.CanIterateElements() {
		// A tags argument that is not a collection at all. The plan will
		// reject it on its own terms with a better message than a merge()
		// this pass wrapped around it would produce.
		return nil, nil, nil, false
	}
	return rw, nil, stringEntries(val), true
}

// literalEntries reads an object constructor's entries by their literal keys,
// and reports whether every key could be read. A single unreadable key makes
// the whole object unusable as a destination, because the marker this pass
// would append could be the one already sitting under it.
func literalEntries(obj *hclsyntax.ObjectConsExpr) (map[string]hclsyntax.Expression, bool) {
	entries := make(map[string]hclsyntax.Expression, len(obj.Items))
	for _, item := range obj.Items {
		key, ok := objectKeyLiteral(item.KeyExpr)
		if !ok {
			return nil, false
		}
		entries[key] = item.ValueExpr
	}
	return entries, true
}

// mergeEntries reads the marker-relevant entries out of a merge() call's
// object-literal arguments, later arguments winning as merge itself makes them
// win. Arguments that are not object literals are invisible here, and the
// marker object this pass appends goes after all of them.
func mergeEntries(call *hclsyntax.FunctionCallExpr) map[string]hclsyntax.Expression {
	entries := make(map[string]hclsyntax.Expression)
	for _, arg := range call.Args {
		obj, ok := arg.(*hclsyntax.ObjectConsExpr)
		if !ok {
			continue
		}
		got, complete := literalEntries(obj)
		if !complete {
			continue
		}
		for k, v := range got {
			entries[k] = v
		}
	}
	return entries
}

// stringEntries reads a statically evaluated tags value as plain strings,
// skipping anything that is not one.
func stringEntries(val cty.Value) map[string]string {
	if val.IsNull() || !val.IsKnown() || !val.CanIterateElements() {
		return nil
	}
	out := make(map[string]string)
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if k.Type() != cty.String || k.IsNull() || v.IsNull() || !v.IsKnown() || v.IsMarked() || v.Type() != cty.String {
			continue
		}
		out[k.AsString()] = v.AsString()
	}
	return out
}

// objectKeyLiteral extracts an object-constructor key's literal string, for
// the two forms a tag key is ever written in: a bare identifier (tofu-estate =
// ..., legal because HCL identifiers may contain hyphens) and a quoted
// constant ("tofu-estate" = ...).
func objectKeyLiteral(keyExpr hclsyntax.Expression) (string, bool) {
	if kw := hcl.ExprAsKeyword(keyExpr); kw != "" {
		return kw, true
	}
	val, diags := keyExpr.Value(nil)
	if diags.HasErrors() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}

// addressExpr builds the tofu-address value for a resource block: a constant
// for a single instance, and a template over the instance key for count and
// for_each. The escaping is [discovery.EscapeAddress] in every branch,
// including the two where it is applied to a bare "[" to produce the ":" that
// introduces an index - so the one implementation of MARKERS.md's escaping
// rule is the one discovery compares with.
//
// The for_each branch interpolates each.key raw, which is correct exactly
// because the key set is bounded elsewhere. OpenTofu renders an instance key
// into an address through addrs' toHCLQuotedString, which adds backslash
// escapes the raw interpolation does not; every character that makes the two
// sides differ is outside the for_each key set the subset admits
// (internal/live/lint, RuleForEachKey, enforced again at expansion time
// in internal/live/identity). Escaping it here instead is not an
// option: an HCL expression cannot reproduce toHCLQuotedString - replace()
// cannot condition on what follows a "$", nor emit \uXXXX for a
// non-printable rune. The invariant is asserted both ways in
// foreach_escape_test.go; if the key rule is ever loosened, that test is
// where it fails.
//
// The second return is the value in operator-readable form, and the third is
// whether it varies per instance.
func addressExpr(rc *configs.Resource, modInst addrs.ModuleInstance) (hclsyntax.Expression, string, bool) {
	base := addrs.ConfigResource{Module: modInst.Module(), Resource: rc.Addr()}.String()
	rng := rc.DeclRange

	switch {
	case rc.Count != nil:
		prefix := discovery.EscapeAddress(base + "[")
		return instanceTemplate(prefix, countIndexTraversal(rng), rng), prefix + "count.index", true
	case rc.ForEach != nil:
		prefix := discovery.EscapeAddress(base + `["`)
		return instanceTemplate(prefix, eachKeyTraversal(rng), rng), prefix + "each.key", true
	default:
		escaped := discovery.EscapeAddress(base)
		return &hclsyntax.LiteralValueExpr{Val: cty.StringVal(escaped), SrcRange: rng}, escaped, false
	}
}

// ---------------------------------------------------------------------------
// tofu-address continuation tags (issue #71)
// ---------------------------------------------------------------------------

// splitAddressMarker turns [addressExpr]'s single tofu-address value into
// the marker entries that carry it: one entry, unchanged, for an address
// that fits a single tag value - the only case before continuation tags
// existed, and still the overwhelming common one - or several, in order
// (tofu-address, tofu-address-2, ...), for one that does not. See
// live/MARKERS.md, "tofu-address continuation tags".
//
// The two shapes addressExpr can return are split two different ways. A
// constant address (no count, no for_each) is split exactly, from its own
// known length: [constantAddressMarkers]. A per-instance address is a
// template over count.index or each.key, and no single instance's length is
// known until the plan evaluates it, so the block's longest possible
// instance decides how many chunks the WHOLE block's tags argument carries -
// [stamper.chunkCount], mirroring internal/live/lint's own static read of
// the same count or for_each expression - and every chunk beyond the first
// is a substr() window over that one template, wrapped once per chunk in
// [templateChunkMarkers]. A shorter instance in the same block simply gets
// empty-string continuations, which [discovery.GatherAddress] safely
// contributes nothing to on read.
func (s *stamper) splitAddressMarker(ctx context.Context, rc *configs.Resource, modInst addrs.ModuleInstance, full hclsyntax.Expression, display string, perInstance bool) []marker {
	rng := rc.DeclRange

	if !perInstance {
		lit, ok := full.(*hclsyntax.LiteralValueExpr)
		if !ok {
			// addressExpr's non-perInstance branch always returns a literal;
			// this is a defensive fallback, not a reachable case.
			return []marker{{key: TagAddress, expr: full, want: display}}
		}
		return constantAddressMarkers(lit.Val.AsString(), rng)
	}

	n := s.chunkCount(ctx, rc, modInst)
	if n <= 1 {
		return []marker{{key: TagAddress, expr: full, want: display, perInstance: true}}
	}
	return templateChunkMarkers(full, display, n, rng)
}

// constantAddressMarkers is [stamper.splitAddressMarker] for a resource with
// neither count nor for_each: the address is a compile-time constant, so
// splitting it is exact rather than a worst-case estimate - one literal
// chunk per tag, no substr(), and a short address (still the common case)
// comes back exactly as it always did: one marker, unchanged.
func constantAddressMarkers(escaped string, rng hcl.Range) []marker {
	chunks := discovery.SplitAddress(escaped)
	out := make([]marker, 0, len(chunks))
	for i, chunk := range chunks {
		out = append(out, marker{
			key:  discovery.AddressTagKey(i),
			expr: &hclsyntax.LiteralValueExpr{Val: cty.StringVal(chunk), SrcRange: rng},
			want: chunk,
		})
	}
	return out
}

// templateChunkMarkers is [stamper.splitAddressMarker] for a per-instance
// address whose longest instance needs n > 1 tags. full is reused unmodified
// as the first argument of n independent substr() calls, one per
// markers.MaxTagValue-sized window; sharing the one hclsyntax node across
// them is safe because this tree is only ever evaluated (Value(ctx)), never
// mutated or re-serialized to source - see stamp/doc.go, "The seam:
// configuration synthesis, before the plan runs". The last chunk's window
// has no upper bound (length -1, "the rest of the string") rather than
// another fixed MaxTagValue, so a shorter instance's trailing chunks come
// back empty rather than error: cty's substr() clamps an out-of-range
// offset to "", it does not fail.
func templateChunkMarkers(full hclsyntax.Expression, display string, n int, rng hcl.Range) []marker {
	out := make([]marker, 0, n)
	for i := 0; i < n; i++ {
		length := discovery.MaxTagValue
		if i == n-1 {
			length = -1
		}
		out = append(out, marker{
			key: discovery.AddressTagKey(i),
			expr: &hclsyntax.FunctionCallExpr{
				Name: "substr",
				Args: []hclsyntax.Expression{
					full,
					&hclsyntax.LiteralValueExpr{Val: cty.NumberIntVal(int64(i * discovery.MaxTagValue)), SrcRange: rng},
					&hclsyntax.LiteralValueExpr{Val: cty.NumberIntVal(int64(length)), SrcRange: rng},
				},
				NameRange:       rng,
				OpenParenRange:  rng,
				CloseParenRange: rng,
			},
			want:        fmt.Sprintf("%s (chunk %d of %d)", display, i+1, n),
			perInstance: true,
		})
	}
	return out
}

// chunkCount is how many tofu-address tags a per-instance resource's longest
// instance needs, mirroring internal/live/lint's own static read of the same
// count or for_each expression (lint's staticCount and staticForEachKeys):
// lint's RuleOverlongAddress has already refused anything the static
// evaluator agrees is over markers.MaxAddressLen by the time a configuration
// reaches this package. An expression this pass cannot evaluate here - the
// same "skip rather than guess" boundary lint itself practices for it -
// returns 1, the pre-#71 behavior: a single tofu-address template, exactly
// as before continuation tags existed. That is a known, pre-existing gap
// shared with lint's own overlong check, not a new one this package opens.
func (s *stamper) chunkCount(ctx context.Context, rc *configs.Resource, modInst addrs.ModuleInstance) int {
	base := addrs.ConfigResource{Module: modInst.Module(), Resource: rc.Addr()}.String()

	var prefix string
	var longest int
	switch {
	case rc.Count != nil:
		n, ok := s.staticCount(ctx, rc)
		if !ok || n < 1 {
			return 1
		}
		prefix = discovery.EscapeAddress(base + "[")
		longest = len(strconv.Itoa(n - 1))
	case rc.ForEach != nil:
		keys, ok := s.staticForEachKeys(ctx, rc)
		if !ok {
			return 1
		}
		prefix = discovery.EscapeAddress(base + `["`)
		for _, k := range keys {
			if l := len([]rune(k)); l > longest {
				longest = l
			}
		}
	default:
		return 1
	}
	return chunksFor(len([]rune(prefix)) + longest)
}

// chunksFor is how many markers.MaxTagValue-sized tags an escaped value of n
// characters needs, capped at markers.MaxContinuations: lint's
// RuleOverlongAddress has already refused anything past
// markers.MaxContinuations*markers.MaxTagValue characters by the time a
// configuration reaches this package, so the cap here is a defensive
// backstop, not a decision this package makes on its own.
func chunksFor(n int) int {
	c := (n + discovery.MaxTagValue - 1) / discovery.MaxTagValue
	if c < 1 {
		c = 1
	}
	if c > discovery.MaxContinuations {
		c = discovery.MaxContinuations
	}
	return c
}

// staticCount is stamp's own copy of the question lint's staticCount asks:
// the value of a count expression, or "not computable here". Two
// independent implementations of the same narrow static-evaluation
// question, matching the precedent this package and lint already set for it
// ([stamper.staticValue] here, lint's own evalStatic there) rather than a
// shared package neither imports.
func (s *stamper) staticCount(ctx context.Context, rc *configs.Resource) (int, bool) {
	if rc.Count == nil || s.mod == nil || s.mod.StaticEvaluator == nil {
		return 0, false
	}
	for _, trav := range rc.Count.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return 0, false
		}
	}

	val, ok := s.evalStatic(ctx, rc.Count, "count")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return 0, false
	}

	var n int
	if err := gocty.FromCtyValue(val, &n); err != nil {
		return 0, false
	}
	return n, true
}

// staticForEachKeys is stamp's own copy of lint's staticForEachKeys: the
// instance keys a for_each expression produces, or "not computable here".
// See [stamper.staticCount]'s doc comment for why this duplicates rather
// than imports.
func (s *stamper) staticForEachKeys(ctx context.Context, rc *configs.Resource) ([]string, bool) {
	if rc.ForEach == nil || s.mod == nil || s.mod.StaticEvaluator == nil {
		return nil, false
	}
	for _, trav := range rc.ForEach.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return nil, false
		}
	}

	val, ok := s.evalStatic(ctx, rc.ForEach, "for_each")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return nil, false
	}

	ty := val.Type()
	var keys []string
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		for it := val.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.Type() != cty.String || k.IsNull() {
				return nil, false
			}
			keys = append(keys, k.AsString())
		}
	case ty.IsSetType(), ty.IsListType(), ty.IsTupleType():
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.Type() != cty.String || v.IsNull() {
				return nil, false
			}
			keys = append(keys, v.AsString())
		}
	default:
		return nil, false
	}
	return keys, true
}

// evalStatic evaluates an expression through the module's static evaluator,
// degrading to "cannot check" instead of erroring - the same recover lint's
// own evalStatic has, for the same reason: the static scope's data source
// panics rather than errors for a reference class the traversal pre-filters
// in [stamper.staticCount] and [stamper.staticForEachKeys] did not already
// turn away.
func (s *stamper) evalStatic(ctx context.Context, expr hcl.Expression, subject string) (val cty.Value, ok bool) {
	defer func() {
		if recover() != nil {
			val, ok = cty.NilVal, false
		}
	}()

	ident := configs.StaticIdentifier{
		Module:    s.modInst.Module(),
		Subject:   subject,
		DeclRange: expr.Range(),
	}
	v, diags := s.mod.StaticEvaluator.Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	return v, true
}

// slotExpr builds the tofu-slot value for a count block: a lookup into the
// slot table this run computed from the live set, keyed by the instance's own
// escaped address.
//
//	tofu-slot = lookup({ "aws_eip.pool:0" = "0", "aws_eip.pool:1" = "3" }, "aws_eip.pool:${count.index}", "")
//
// The table is the assignment; count.index only picks a row out of it. That
// distinction is the whole reason a slot can be written from configuration at
// all: `tofu-slot = count.index` would make the lexical position the identity,
// which is the one thing live/MARKERS.md says a slot is not, whereas this
// says "whatever slot this run decided instance k holds" and the deciding
// happened in the set matcher, from live tags.
//
// The default is the empty string, which no member of the table can be: a
// count index outside the table would mean this run stamped a table that does
// not cover the configuration it is stamping, and an empty tag is a visible
// wrong answer rather than a plausible one.
//
// The three returns are the expression, the value in operator-readable form,
// and whether there is a table to write at all. There is not when the resource
// has no count, when discovery never ran, or when discovery assigned this
// block nothing - and in all three cases the resource gets no tofu-slot,
// which is what every pre-slot estate looked like.
func (s *stamper) slotExpr(rc *configs.Resource) (hclsyntax.Expression, string, bool) {
	if rc.Count == nil || len(s.req.Slots) == 0 {
		return nil, "", false
	}

	rng := rc.DeclRange
	prefix := discovery.EscapeAddress(addrs.ConfigResource{Module: s.modInst.Module(), Resource: rc.Addr()}.String() + "[")

	keys := make([]string, 0, len(s.req.Slots))
	for key := range s.req.Slots {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, "", false
	}
	// Sorted by the index the key carries, so the table reads in instance
	// order and two runs over one configuration produce identical source.
	sort.Slice(keys, func(i, j int) bool {
		a, aok := indexOf(keys[i], prefix)
		b, bok := indexOf(keys[j], prefix)
		if aok && bok {
			return a < b
		}
		return keys[i] < keys[j]
	})

	items := make([]hclsyntax.ObjectConsItem, 0, len(keys))
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, hclsyntax.ObjectConsItem{
			KeyExpr:   &hclsyntax.ObjectConsKeyExpr{Wrapped: &hclsyntax.LiteralValueExpr{Val: cty.StringVal(key), SrcRange: rng}},
			ValueExpr: &hclsyntax.LiteralValueExpr{Val: cty.StringVal(s.req.Slots[key]), SrcRange: rng},
		})
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, s.req.Slots[key]))
	}

	expr := &hclsyntax.FunctionCallExpr{
		Name: "lookup",
		Args: []hclsyntax.Expression{
			&hclsyntax.ObjectConsExpr{Items: items, SrcRange: rng, OpenRange: rng},
			instanceTemplate(prefix, countIndexTraversal(rng), rng),
			&hclsyntax.LiteralValueExpr{Val: cty.StringVal(""), SrcRange: rng},
		},
		ExpandFinal:     false,
		NameRange:       rng,
		OpenParenRange:  rng,
		CloseParenRange: rng,
	}
	return expr, strings.Join(pairs, " "), true
}

// indexOf reads the count index off an escaped instance address whose block
// prefix is known.
func indexOf(key, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// instanceTemplate is the expression `"<prefix>${<key>}"`, which is exactly
// what an author writes by hand today ("aws_eip.pool:${count.index}") and
// therefore exactly what [sameExpr] recognizes as already correct.
func instanceTemplate(prefix string, key hcl.Traversal, rng hcl.Range) hclsyntax.Expression {
	return &hclsyntax.TemplateExpr{
		Parts: []hclsyntax.Expression{
			&hclsyntax.LiteralValueExpr{Val: cty.StringVal(prefix), SrcRange: rng},
			&hclsyntax.ScopeTraversalExpr{Traversal: key, SrcRange: rng},
		},
		SrcRange: rng,
	}
}

func countIndexTraversal(rng hcl.Range) hcl.Traversal {
	return hcl.Traversal{
		hcl.TraverseRoot{Name: "count", SrcRange: rng},
		hcl.TraverseAttr{Name: "index", SrcRange: rng},
	}
}

func eachKeyTraversal(rng hcl.Range) hcl.Traversal {
	return hcl.Traversal{
		hcl.TraverseRoot{Name: "each", SrcRange: rng},
		hcl.TraverseAttr{Name: "key", SrcRange: rng},
	}
}

// sameExpr reports whether an author's expression is the same instance
// address template this pass would have written. It compares structure, not
// source text, so `"aws_eip.pool:${count.index}"` matches however it was
// spaced or quoted, and anything else - a format() call, a different prefix,
// a different key - does not.
func sameExpr(got, want hclsyntax.Expression) bool {
	switch want := want.(type) {
	case *hclsyntax.LiteralValueExpr:
		gv, diags := got.Value(nil)
		if diags.HasErrors() || gv.IsNull() || !gv.IsKnown() || gv.Type() != cty.String {
			return false
		}
		return gv.RawEquals(want.Val)

	case *hclsyntax.TemplateExpr:
		gt, ok := got.(*hclsyntax.TemplateExpr)
		if !ok || len(gt.Parts) != len(want.Parts) {
			return false
		}
		for i := range want.Parts {
			if !sameExpr(gt.Parts[i], want.Parts[i]) {
				return false
			}
		}
		return true

	case *hclsyntax.ScopeTraversalExpr:
		gt, ok := got.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			return false
		}
		return sameTraversal(gt.Traversal, want.Traversal)

	case *hclsyntax.FunctionCallExpr:
		gt, ok := got.(*hclsyntax.FunctionCallExpr)
		if !ok || gt.Name != want.Name || len(gt.Args) != len(want.Args) {
			return false
		}
		for i := range want.Args {
			if !sameExpr(gt.Args[i], want.Args[i]) {
				return false
			}
		}
		return true

	case *hclsyntax.ObjectConsExpr:
		// Order-sensitive on purpose: this pass writes the table in instance
		// order, so a table in another order is one somebody else wrote, and
		// "the same table shuffled" is not a claim worth making the risk of
		// a false match for.
		gt, ok := got.(*hclsyntax.ObjectConsExpr)
		if !ok || len(gt.Items) != len(want.Items) {
			return false
		}
		for i := range want.Items {
			if !sameObjectKey(gt.Items[i].KeyExpr, want.Items[i].KeyExpr) {
				return false
			}
			if !sameExpr(gt.Items[i].ValueExpr, want.Items[i].ValueExpr) {
				return false
			}
		}
		return true
	}
	return false
}

// sameObjectKey compares two object-constructor keys by the literal string
// each names, so a bare identifier and the same word quoted compare equal -
// which is what they mean.
func sameObjectKey(got, want hclsyntax.Expression) bool {
	g, gok := objectKeyLiteral(got)
	w, wok := objectKeyLiteral(want)
	return gok && wok && g == w
}

func sameTraversal(got, want hcl.Traversal) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		switch w := want[i].(type) {
		case hcl.TraverseRoot:
			g, ok := got[i].(hcl.TraverseRoot)
			if !ok || g.Name != w.Name {
				return false
			}
		case hcl.TraverseAttr:
			g, ok := got[i].(hcl.TraverseAttr)
			if !ok || g.Name != w.Name {
				return false
			}
		default:
			return false
		}
	}
	return true
}
