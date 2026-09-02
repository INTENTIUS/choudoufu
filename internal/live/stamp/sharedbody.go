// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// tagsArgument is the argument the markers go into. It lives here rather than
// as a literal in each reader because [privateBody] has to copy exactly the
// node [stamper.tagsWrite] then reads, and two spellings of the same string
// is the way that stops being true.
const tagsArgument = "tags"

// A module source called more than once is parsed once.
//
// hclparse.Parser caches a parsed file by its name and hands the same
// *hcl.File to every caller, and internal/live/check.Load - like OpenTofu's
// own configload.Loader - uses one configs.Parser for the root module and
// every child. So a local module called seven times, the ordinary way a
// configuration is factored (one call per domain, per environment, per
// service), produces seven *configs.Module values over one syntax tree.
//
// Each call does get its own *configs.Resource, and its own *hclsyntax.Body
// for the resource's configuration: hclsyntax.Body.PartialContent allocates
// a fresh "remain" body per call, which is what configs.decodeResourceBlock
// stores in Resource.Config. What it does NOT allocate is a new Attributes
// map - the remain body points at the original's - so every one of the seven
// calls reaches one hclsyntax.Attributes map, one tags attribute and one
// tags expression.
//
// This pass writes a literal tofu-address into that tags argument. Written
// once per call into shared nodes, the last call's address is the one all
// seven resources evaluate, and the estate applies cleanly with seven live
// objects claiming one identity - GitHub issue #280, found on
// .corpus/simpleinfra/terraform/dns, where all seven hosted zones came back
// carrying module.rustconf_com.aws_route53_zone.zone.
//
// The sharing is a property of the loader, not of the configuration: the
// seven calls are seven independent module instances that happen to have
// been read from one file. So the fix is to undo the sharing for the nodes
// this pass writes to, which is what privateBody does, rather than to refuse
// a shape stock OpenTofu plans without complaint.

// privateBody gives one resource's configuration body its own copy of every
// node [rewrite.apply] can mutate, so that a marker written for one module
// call cannot land on another call of the same source.
//
// The four nodes are the four shapes in [rewrite], and they are the whole
// list:
//
//   - rewrite.body: apply installs a whole tags attribute into
//     body.Attributes when the resource declares none.
//   - rewrite.wrap: apply replaces the tags attribute's expression.
//   - rewrite.obj: apply appends items to the tags object literal.
//   - rewrite.merge: apply appends an argument to the tags merge() call.
//
// Nothing below the tags argument's own top-level expression is ever
// mutated, so nothing below it is copied. [TestPrivateBodyCoversEveryRewriteShape]
// is what keeps that claim true if a fifth shape is added.
//
// It runs on every resource, not only on one whose body it can prove is
// shared. Proving it would mean detecting the sharing, and detection is
// exactly the part that has to be right for the guarantee to hold: two
// registry calls resolving to one .terraform/modules directory, a symlinked
// source, a future loader that caches differently. Copying unconditionally
// needs no such judgement, and a copy of a body nobody else holds is
// indistinguishable from the body.
func privateBody(body *hclsyntax.Body) {
	// maps.Clone(nil) is nil, and apply installs a fresh map on this body -
	// which is already this call's own - when it finds one.
	body.Attributes = maps.Clone(body.Attributes)

	attr := body.Attributes[tagsArgument]
	if attr == nil {
		return
	}
	private := *attr
	switch expr := private.Expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		obj := *expr
		obj.Items = slices.Clone(expr.Items)
		private.Expr = &obj
	case *hclsyntax.FunctionCallExpr:
		call := *expr
		call.Args = slices.Clone(expr.Args)
		private.Expr = &call
	}
	body.Attributes[tagsArgument] = &private
}

// claimBody records that addr is about to rewrite body, and refuses when
// some other resource already claimed the same one.
//
// [privateBody] rests on a fact about the loader: two module calls of one
// source get two *hclsyntax.Body values, and only what hangs off them is
// shared. If that ever stops holding - a loader that hands the raw parsed
// body to every call, an override path, a change upstream - then copying
// body.Attributes for the second call would overwrite the first call's copy
// and issue #280 would be back, silently, with every offline instrument
// still reading clean.
//
// So the precondition is asserted rather than assumed. A body claimed twice
// is refused outright, and refusing is the right outcome even though it
// stops a run that used to work: a marker written into a body two resources
// share is wrong for at least one of them, and a wrong marker goes onto a
// real cloud object and can adopt or displace something a missing one
// cannot.
func (s *stamper) claimBody(body *hclsyntax.Body, addr addrs.ConfigResource, rc *configs.Resource) (tfdiags.Diagnostics, bool) {
	var diags tfdiags.Diagnostics

	if prev, taken := s.bodies[body]; taken {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  SummarySharedBody,
			Detail: fmt.Sprintf(
				"%s and %s are backed by one configuration body, so the %s tag written for one of them would be the tag the other carries too. "+
					"Applying that would put a single ownership marker on two different live objects, and the run after it could not tell them apart. "+
					"This is a defect in how this run loaded the configuration, not something the configuration did wrong: report it with the module call that repeats.",
				prev, addr, TagAddress,
			),
			Subject: rc.DeclRange.Ptr(),
		}), false
	}
	if s.bodies == nil {
		s.bodies = make(map[*hclsyntax.Body]addrs.ConfigResource)
	}
	s.bodies[body] = addr
	privateBody(body)
	return diags, true
}
