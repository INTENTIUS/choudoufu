// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The record-orphan-read leg (GitHub issue #364 ruling item 1's removal
// half, found building corpus-hongbomiao-harbor's day2_remove unit): an
// untaggable type whose identity has more than one attribute-supplying
// Component - aws_iam_role_policy, aws_iam_user_policy,
// aws_iam_group_policy, and (since gauntlet:record-located-destroy,
// 2026-08-25) aws_lb_target_group_attachment and its aws_alb_ alias -
// carries no marker of its own AND is excluded from
// [parentReadSweep]/[foldChildReadSweep] on purpose
// ([identity.SingleParentComponent] requires exactly one attribute
// component; [TestParentReadSweepIgnoresMultiComponentTypes] pins the
// exclusion). Neither leg can tell "the child exists" from "the child does
// not" for it, so removing its whole parent's resource block - the harbor
// unit's own module.harbor_iam_user, whose one aws_iam_user carries an
// inline aws_iam_user_policy - left the inline policy destroy unproposed:
// the object was correctly recognized as a real resource type, but nothing
// enumerated it.
//
// What this file adds is not a new enumeration route. GitHub issue #364
// unit A2 (internal/live/liveimport/stamp.go's seedIdentityFor,
// internal/live/projection/build.go's discoverOrphanedRecords) already
// seeds every untaggable instance's identity into the estate's own record
// store the moment its live object is confirmed - the SAME store
// [scanTypeLocatedFallback] already reads from, one file over, for a
// DECLARED instance. discoverOrphanedRecords itself already walks that
// store's whole key set looking for undeclared entries, but on purpose
// skips every key whose kind is "identity" rather than "object"
// (build.go's own comment: "a kind=identity key is never delete
// authority") - identity records need a live confirmation read before a
// destroy is safe to propose, the same way a tag-based orphan's ImportID is
// never trusted without classifyOrphans's own withholding pass, and
// discoverOrphanedRecords has no such pass. This leg is that pass, sourced
// from the record store instead of the tag sweep, feeding the exact same
// [identity.Resolution] shape [classifyOrphans] already produces into
// [builder.run]'s existing materialize/import-and-read path - so the live
// confirmation read, the ownership semantics and the destroy ordering are
// all the SAME code every other undeclared instance already goes through,
// not a second one.
package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// recordOrphanReadSweep is the leg's entry point, called once per [Discover]
// pass that asked for a sweep, alongside [parentReadSweep] and
// [foldChildReadSweep]. It costs one record-store List plus one GetIdentity
// per undeclared key - bounded by the estate's own record population, never
// by a type's whole live inventory.
func recordOrphanReadSweep(ctx context.Context, req Request, schemas listclient.Schemas, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.HintStore == nil {
		// No record store was ever opened for this pass (no live block, or
		// one whose store could not be built) - the same "nothing to
		// consult" answer [scanTypeLocatedFallback] gives.
		return diags
	}
	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	store := projection.NewRecordEnvelopeStore(req.HintStore, prefix)

	keys, err := store.List(ctx)
	if err != nil {
		diags = diags.Append(problemDiag(res, Problem{
			Kind:   ProblemRecordStoreListFailed,
			Detail: fmt.Sprintf("Listing the record store to find untaggable resources whose configuration block was removed failed: %s.", err),
		}))
		return diags
	}
	if len(keys) == 0 {
		return diags
	}

	// Every address this pass has already accounted for, one way or
	// another - a declared block, a tag-based orphan classifyOrphans just
	// appended, a parent-read or fold-child finding. A key that decodes to
	// one of these is not this leg's to add a second time.
	known := make(map[string]bool, len(res.Resolutions))
	for _, r := range res.Resolutions {
		known[r.Addr.String()] = true
	}

	// A `moved` block's OLD address, for a DECLARED instance (#405/#410's
	// re-verify wave, 8 estates: giantswarm's iam_role_policy family, sg's
	// rules_exclusive, ec2's route/route_table_association,
	// rds/autoscaling's security_group_rule, dynamodb's resource_policy,
	// s3's public_access_block, overture's 8 types). This leg's record keys
	// are still written under the OLD address until write-back moves them
	// (RecordStore.MoveRecord re-keys directly and never hits this path;
	// only a bare HCL `moved` block leaves a stale key here), so without
	// this, an untaggable record-backed instance mid-rename reads as a
	// removed one and this leg destroys the live object under its old
	// address - the exact defect this unit fixes.
	//
	// Mirrors the two existing consult points rather than inventing a
	// third: declaredInstances (discovery.go, the marker path's own
	// rename-safety index) files each declared entry under
	// moved.Aliases(movedStmts, r.Addr) so a marker still naming the old
	// address binds the instance instead of orphaning it, and
	// builder.locatedIdentityWithAliases (projection/located.go) does the
	// identical walk to find a record-located instance's identity under an
	// old key. Only genuinely DECLARED entries (r.Undeclared == false)
	// contribute an alias - an orphan or parent-read finding this same pass
	// already appended has no configuration block to be a rename target of,
	// exactly the population declaredInstances itself draws from
	// (req.Resolutions, the caller's declared list, not res.Resolutions
	// after sweeps have appended to it).
	movedStmts := moved.Honoured(req.Config)
	if len(movedStmts) > 0 {
		for _, r := range res.Resolutions {
			if r.Undeclared {
				continue
			}
			for _, origin := range moved.Aliases(movedStmts, r.Addr) {
				known[origin.String()] = true
			}
		}
	}

	// The SAME rename-safety check [classifyOrphans] applies before
	// proposing anything: a resource block that still has an unclaimed
	// declared instance means this address may be the same object under a
	// new instance key, not a genuine removal - see [classifyOrphans]'s own
	// doc comment for why that check comes before every other one.
	pending := make(map[string]bool, len(res.Unbound))
	for _, addr := range res.Unbound {
		pending[blockKey(addr)] = true
	}

	// The root module's own `markers "record"` selection (see
	// [identity.SelectionFor]'s doc comment) - read once, here, the same
	// way internal/live/stamp and internal/live/lint each read it
	// independently rather than threading it through. It is a ROOT-module
	// fact, addressed by type or by [addrs.ConfigResource] string, so it
	// answers for an address whose declaring block this same pass just
	// deleted exactly as it would for one still declared: neither form
	// depends on the resource block existing in req.Config, only on the
	// root live block's own two lists.
	selection := identity.SelectionFor(req.Config)

	for _, key := range keys {
		addr, ok := projection.RecordAddr(prefix, key)
		if !ok {
			// A key this package cannot make sense of - not a promise every
			// key in the store's namespace is one of ours; skipped rather
			// than failed, the same discipline
			// [builder.discoverOrphanedRecords] applies to its own keys.
			continue
		}
		if known[addr.String()] {
			continue
		}
		typeName := addr.Resource.Resource.Type
		if _, ok := identity.LookupType(typeName); !ok {
			// Not an admitted type at all - a stray or foreign key, not
			// this leg's to guess at, the same discipline
			// [builder.discoverOrphanedRecords] applies to a key it cannot
			// make sense of.
			continue
		}
		if typeTaggable(schemas, typeName) && !selection.Selects(addr.ConfigResource()) {
			// Taggable, meaning the ordinary tag sweep already covers it
			// (and already ran, above, before this leg) and would already
			// be in known if it found anything. [typeTaggable] reads the
			// managed resource schema via [listclient.Schemas.ResourceSchema]
			// rather than [Schemas.Get] on purpose - this leg's whole
			// population (aws_iam_role_policy and its two IAM siblings
			// today) has no list route, so [Schemas.Get] would report
			// every one of them not-ok and this check would never fire.
			//
			// That is true for a MARKED instance of a taggable type. It is
			// NOT true for one this estate's own `markers "record"`
			// selection has opted out of a tag - by type or by address,
			// [strict.Selection.Selects]'s two forms - because that
			// instance never carried a tag for the sweep to find in the
			// first place; typeTaggable answers "could this type ever
			// carry a marker", not "did THIS instance get one", and the
			// two diverge exactly under a selection. Found building
			// corpus-sumaform-aws's day2_remove unit
			// (module.server.aws_instance.instance and
			// .aws_ebs_volume.data_disk, both selected by type in this
			// estate's own root `strict { markers "record" { types = [...
			// ] } }` block): deleting module.server's whole block left both
			// instances invisible to the tag sweep (never tagged) AND to
			// this leg (taggable type, so skipped) at once. Generic:
			// reaches every type an estate selects into markers=record,
			// not aws_instance specifically - selection.Selects reads the
			// root live block's own lists, not a hand-wired type name.
			continue
		}
		if pending[blockKey(addr)] {
			// A declared instance of the same block is unclaimed: this may
			// be a rename with no moved block, not a removal. Leave it for
			// the rename section, exactly as classifyOrphans does for a
			// tag-found orphan at the same address.
			continue
		}

		rec, _, keyExists, identityFound, err := store.GetIdentity(ctx, addr)
		if err != nil {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemLocatedRecordUnreadable,
				TypeName: typeName,
				Addr:     addr,
				Detail:   fmt.Sprintf("Reading the persisted record for %s, to decide whether it is safe to propose destroying, failed: %s.", addr, err),
			}))
			continue
		}
		if !keyExists || !identityFound {
			// Not a kind=identity record at all - a kind=object record is
			// [builder.discoverOrphanedRecords]'s own population, and a key
			// this store cannot decode is not this leg's to guess at.
			continue
		}
		importID := rec.ImportID
		if importID == "" {
			// A composite identity from the provider's own wire identity
			// schema ([projection.LocatedRecord.Components] populated
			// instead of a flat ImportID - [projection.LocatedRecordFrom]'s
			// Composite() case, taken when [identity.RecordableIdentitySchema]
			// is true for the type, as opposed to the flat string
			// locatedRatifiedComponentsRecord already produces when it is
			// false. Both are the SAME logical identity, read back from
			// different write shapes - found empirically: aws_iam_role_policy
			// and aws_iam_user_policy share one ratified table row shape but
			// wrote two different record shapes, one flat and one composite,
			// on the SAME provider version.
			var ok bool
			importID, ok = composeImportIDFromComponents(typeName, rec.Components)
			if !ok {
				// A component this composer does not know how to resolve (a
				// nested Block, a Default substitute, an OmitIfAbsent
				// segment) or an attribute the record's Components map does
				// not carry. Refusing to guess which component means what,
				// rather than flattening one arbitrarily, is the same
				// discipline [scanTypeLocatedFallback] applies to its own
				// composite records.
				continue
			}
		}

		var dependsOn []addrs.AbsResourceInstance
		if len(rec.Components) > 0 {
			// Only when this record's own Components map is in hand - the
			// composite-identity write path every aws_route53_record
			// instance takes (see composeImportIDFromComponents's own
			// comment) - never for the flat-ImportID path the three IAM
			// inline-policy types take, whose own linking argument (role/
			// user/group) was never captured as a separate value to read
			// back out of. See [destroyParentDependency]'s own doc comment
			// for why this exists at all.
			dependsOn = destroyParentDependency(req, schemas, res, typeName, rec.Components)
		}

		res.Resolutions = append(res.Resolutions, identity.Resolution{
			Addr:             addr,
			Class:            identity.ClassConcrete,
			ImportID:         importID,
			Undeclared:       true,
			DestroyDependsOn: dependsOn,
		})
		known[addr.String()] = true
	}

	return diags
}

// destroyParentDependency finds, among this pass's own resolutions, the
// live parent instance typeName's ratified Components chain names via
// [identity.ParentOf] - reusing the SAME derivation [parentReadSweep]
// already relies on, never a second, type-specific rule - so an undeclared
// record's own materialized object can carry the SAME destroy-before-
// parent ordering [builder.dependencies] gives a declared instance from
// its own configuration reference (internal/live/projection/build.go).
//
// Found via corpus-mastino-dns's day2_remove: aws_route53_zone's own
// force_destroy semantics cascade-delete the zone's apex NS record the
// moment the zone itself is destroyed, so a genuinely undeclared instance
// with no computed dependency set (every undeclared instance's ordinary
// state - see [identity.Resolution.DestroyDependsOn]'s own doc comment)
// can propose the CORRECT destroy in the plan and still fail at apply
// time: the record's own destroy call, issued after the zone is already
// gone, returns NoSuchHostedZone. Stock's state file never has this
// problem - it remembers the record depended on the zone from the
// original apply, and destroys in reverse order - so this closes a real
// gap against the oracle, not a cosmetic one.
//
// Nil when the type has no derivable parent link, the record's own
// Components map does not carry that link's value, or no resolution in
// this pass names a live object at that value - none of which is an
// error: it just means this leg cannot supply an ordering hint, exactly
// the "no computed dependency set" cost [builder.dependencies]'s own doc
// comment already accepts for an undeclared instance in general. Reaches
// every type [identity.ParentOf] can link to an eligible (admitted,
// taggable) parent, not only aws_route53_record.
func destroyParentDependency(req Request, schemas listclient.Schemas, res *Result, typeName string, components map[string]string) []addrs.AbsResourceInstance {
	links := identity.ParentOf(typeName, taggableAdmittedTypes(schemas), rosterServiceOf(req.Roster))
	for _, link := range links {
		val, ok := components[link.Attr]
		if !ok || val == "" {
			continue
		}
		for _, r := range res.Resolutions {
			if r.Type() == link.Parent && r.ImportID == val {
				return []addrs.AbsResourceInstance{r.Addr}
			}
		}
	}
	return nil
}

// composeImportIDFromComponents flattens a [projection.LocatedRecord]'s
// Components map (attribute name to value, from the provider's own wire
// identity schema) into the import-ID string identity.LookupType(typeName)'s
// own ratified Components describe - the same grammar
// [projection.LocatedRecordFrom]'s locatedRatifiedComponentsRecord path
// already flattens at write time for a type the provider serves no wire
// identity schema for. Nothing else in this codebase re-joins the OTHER
// shape back into a string: every existing reader either has the concrete
// cty object in hand (rendering straight from it) or, like
// [scanTypeLocatedFallback], only ever reaches a single-attribute type.
//
// Deliberately narrow: a component naming a nested Block or a documented
// Default substitute is not composed - the record's Components map carries
// neither a nested value nor a marker for "this was the default", so
// guessing either would be exactly the approximation this function's own
// doc comment refuses to make. Every OTHER component shape IS composed,
// [identity.Component.OmitIfAbsent] included: an OmitIfAbsent component
// contributes its Literal-plus-value when the record's Components map
// carries it and contributes nothing at all - not even its own Literal -
// when it does not, mirroring [resolver.resolveInstance]'s own
// "absence is not a hard refusal" handling of the identical field
// (internal/live/identity/resolve.go's OmitIfAbsent branch) rather than
// re-deriving a second account of what OmitIfAbsent means.
//
// Until 2026-08-25 this function refused outright the instant it saw ANY
// OmitIfAbsent component anywhere in the ratified row, whether or not that
// component's own value was present in the record - the check tested the
// ROW's shape, not the RECORD's content. That was sound while the only rows
// reaching this leg were aws_iam_role_policy, aws_iam_user_policy and
// aws_iam_group_policy, none of which has an OmitIfAbsent component at all,
// so the distinction never mattered. It stopped being sound the moment
// aws_lb_target_group_attachment reached this leg (corpus-alb-complete's
// day2_remove unit, gauntlet:record-located-destroy): every instance of the
// type, port present or not, was silently refused at the row's third
// component (Attrs: []string{"port"}, OmitIfAbsent: true) before this
// function ever looked at what the record actually held, so the whole type
// composed to nothing and its destroy was never proposed - not a design
// exclusion, a bug in this loop. Measured against live/survey-full.json's
// ratified table (2026-08-25): 5 rows carry an OmitIfAbsent component today
// (aws_alb_target_group_attachment, aws_lambda_permission,
// aws_lb_target_group_attachment, aws_route53_record,
// aws_route53_zone_association), so this fix reaches all five, not one
// named type.
func composeImportIDFromComponents(typeName string, components map[string]string) (string, bool) {
	entry, ok := identity.LookupType(typeName)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, c := range entry.Components {
		if c.Block != "" || c.Default != "" {
			return "", false
		}
		if len(c.Attrs) == 0 {
			b.WriteString(c.Literal)
			continue
		}
		val, found := "", false
		for _, attr := range c.Attrs {
			if v, ok := components[attr]; ok && v != "" {
				val, found = v, true
				break
			}
		}
		if !found {
			if c.OmitIfAbsent {
				// See [Component.OmitIfAbsent]: absent means this segment,
				// literal prefix included, contributes nothing at all - not
				// a reason to refuse composing the rest of the identity.
				continue
			}
			return "", false
		}
		b.WriteString(c.Literal)
		b.WriteString(val)
	}
	return b.String(), true
}
