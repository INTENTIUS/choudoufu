// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The parent-scoped Cloud Control leg. It exists for a shape [parent_read.go]
// cannot express and [scanTypeCloudControl] cannot reach: a type whose
// identity is a server-minted own component under a config-known parent -
// AWS::ApiGateway::Resource's ResourceId under a RestApiId, for example -
// where Cloud Control's own registry (live/registry.json's
// handlers.list_required_input) says a listing is possible, but only when
// scoped to one parent at a time.
//
// This is a genuinely different shape from [identity.SingleParentComponent]'s
// "named-singleton child" model that [parentReadSweep] already covers: that
// one settles a child's whole identity from its parent alone (a bucket
// policy is 0-or-1 per bucket, and reading the parent answers "does it
// exist" outright). Here a parent legitimately has zero, one, or many
// children - a REST API can have any number of resources, deployments,
// stages - so the read is a real list, not a singleton check, and every
// match found under a parent (not just the first) has to be reported. See
// [identity.SingleParentComponent]'s own doc comment for the
// attrComponents == 1 condition this shape does not satisfy: none of the
// composite types this file was built for compose to a single-attribute
// identity, because their own component is not a configuration argument at
// all.
//
// What this file cannot do on its own is decide, for a given admitted type,
// which parent to scope by and which of its own configuration arguments (if
// any - the scoping value may not appear in configuration verbatim) carries
// the parent's value. That is an identity-table fact
// (internal/live/identity), and no vocabulary for "one component is
// config-known, the other is server-assigned and list-discovered" exists
// there today - [identity.TypeIdentity.Components] composes an identity
// entirely from configuration or refuses ([identity.TypeIdentity.ServerAssigned]
// covers only a type whose whole identity is server-minted, no parent
// component at all). So every function here takes the parent link as an
// explicit [ParentScopedChildSpec] rather than deriving it, and nothing in
// this package's [Discover] pipeline calls them yet - see live/LIMITATIONS.md
// and the issue this shipped against for what a caller supplying that
// vocabulary would need to add.
package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ParentScopedChildSpec is what a caller supplies to drive one type through
// the parent-scoped Cloud Control leg - the identity-table fact this
// package's own admission table does not have a vocabulary for yet (see the
// package doc).
type ParentScopedChildSpec struct {
	// TypeName is the child's Terraform resource type, e.g.
	// "aws_api_gateway_resource".
	TypeName string

	// Parent is the admitted, taggable parent type whose resolved instances
	// this leg iterates - e.g. "aws_api_gateway_rest_api". Every concrete
	// resolution of this type in [Request.Resolutions] is one scoping value
	// to list children under.
	Parent string

	// CFNScopeProperty is the single CFN property name
	// live/registry.json's handlers.list_required_input names for the
	// child's CFN type - e.g. "RestApiId". This leg refuses to run when the
	// registry's own required-input list is not exactly this one property
	// (see [SweepGapScopeUnavailable]): a type needing more than one
	// scoping value, or a different one, is not this shape and this leg
	// must not guess which of several inputs the parent's value belongs
	// under.
	CFNScopeProperty string
}

// parentScopedCloudControlSweepType runs one [ParentScopedChildSpec] against
// every parent instance this pass resolved. It is additive to
// [parentReadSweep]: nothing here changes what that leg already covers,
// because a caller only ever builds a spec for a type
// [identity.SingleParentComponent] cannot describe in the first place.
func parentScopedCloudControlSweepType(ctx context.Context, req Request, spec ParentScopedChildSpec, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.CloudControl == nil || req.Roster == nil {
		// Nothing to call without both. Not a gap: a run that never
		// configured Cloud Control or a roster gets no coverage from any
		// CFN-backed leg, [scanTypeCloudControl] included, and is silent
		// about it the same way for the same reason - see
		// [cloudControlSource].
		return diags
	}

	cfnType, requiredInput, ok := req.Roster.EnumerationSourceScoped(spec.TypeName)
	if !ok {
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: spec.TypeName,
			Reason:   SweepGapNoRegistryRow,
			Detail: fmt.Sprintf(
				"%s is not mapped to a Cloud-Control-listable, required-input CFN type in live/mapping.json + live/registry.json, so the parent-scoped Cloud Control leg has nothing to call.",
				spec.TypeName),
		}))
	}
	if len(requiredInput) != 1 || requiredInput[0] != spec.CFNScopeProperty {
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: spec.TypeName,
			Reason:   SweepGapScopeUnavailable,
			Detail: fmt.Sprintf(
				"%s (Cloud Control type %s) requires list input %v, which does not match the single scoping property %q this leg was given for parent %s - refusing rather than guessing which required input the parent's value belongs under.",
				spec.TypeName, cfnType, requiredInput, spec.CFNScopeProperty, spec.Parent),
		}))
	}

	scopeIndex, verifiable := scopePropertyIndex(req.Roster.PrimaryIdentifier(cfnType), spec.CFNScopeProperty)
	arity := req.Roster.IdentifierArity(cfnType)
	if !verifiable {
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: spec.TypeName,
			Reason:   SweepGapScopeUnavailable,
			Detail: fmt.Sprintf(
				"%s (Cloud Control type %s) requires list input %q to scope, but that property does not appear in the type's own primary_identifier (%v), so a scoped listing's results cannot be verified as actually belonging to the parent they were scoped to - see internal/live/cloudcontrol.Client.ListResourcesScoped's own warning that not every Cloud Control backend honors ResourceModel scoping (floci's own implementation does not). Refusing rather than trusting an unverifiable result.",
				spec.TypeName, cfnType, spec.CFNScopeProperty, req.Roster.PrimaryIdentifier(cfnType)),
		}))
	}

	// Composed identity, not ImportID: the same declared set the parent-read
	// legs use ([declaredChildImportIDs]).
	declared := declaredChildImportIDs(spec.TypeName, res)

	var callFailures []string
	for _, r := range res.Resolutions {
		if r.Type() != spec.Parent || r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		if modCfg, ok := identity.ConfigForModule(req.Config, r.Addr.Module); ok && modCfg.Module != nil {
			if rc, ok := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]; ok && !inScope(req.ScopeProvider, rc, modCfg) {
				// Issue #69's multi-provider sweep: this parent belongs to a
				// different provider configuration, the same restraint
				// [parentReadSweepType] already applies.
				continue
			}
		}
		parentValue := r.ImportID

		descs, err := req.CloudControl.ListResourcesScoped(ctx, cfnType, map[string]string{spec.CFNScopeProperty: parentValue})
		if err != nil {
			callFailures = append(callFailures, fmt.Sprintf("%s: %v", parentValue, err))
			continue
		}

		for _, d := range descs {
			if !identifierMatchesParent(d.Identifier, scopeIndex, arity, parentValue) {
				// The backend either ignored the scope entirely (floci) or
				// returned something whose shape does not match what the
				// registry documents for this type - either way, accepting
				// it would risk attributing one parent's child to another.
				// Not fatal to the whole call: the rest of this page's
				// results may still be genuinely scoped, so each is judged
				// on its own identifier rather than discarding the batch.
				continue
			}
			importID, idOK := resolveCloudControlImportID(spec.TypeName, d.Identifier)
			if !idOK {
				diags = diags.Append(problemDiag(res, Problem{
					Kind:     ProblemUncomposableIdentifier,
					TypeName: spec.TypeName,
					LiveIDs:  liveIDs(d.Identifier),
					Detail: fmt.Sprintf(
						"Cloud Control returned the multi-part identifier %q for a %s (parent-scoped under %s=%s), and no entry in the identity table (internal/live/identity) can compose its parts into a TF import identity.",
						d.Identifier, spec.TypeName, spec.CFNScopeProperty, parentValue),
				}))
				continue
			}
			if declared[importID] {
				continue
			}
			res.ParentReads = append(res.ParentReads, ParentReadFinding{
				TypeName:     spec.TypeName,
				Parent:       spec.Parent,
				ParentAddr:   r.Addr,
				ParentValue:  parentValue,
				ImportID:     importID,
				IdentityAttr: "id",
				Withheld: fmt.Sprintf(
					"%s is reachable via a parent-scoped Cloud Control listing under %s but this leg does not yet remove it - see live/LIMITATIONS.md, \"Some are swept via a parent read instead (issue #60)\".",
					spec.TypeName, spec.Parent),
			})
		}
	}

	if len(callFailures) > 0 {
		diags = diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: spec.TypeName,
			Reason:   SweepGapListFailed,
			Detail: fmt.Sprintf(
				"Parent-scoped Cloud Control ListResources for %s (%s) failed for %d parent instance(s): %s. Live children under those parents may be missing from this estate's removal-detection sweep.",
				spec.TypeName, cfnType, len(callFailures), strings.Join(callFailures, "; ")),
		}))
	} else {
		res.SweepCovered = append(res.SweepCovered, spec.TypeName)
	}
	return diags
}

// scopePropertyIndex finds where scopeProperty sits in a CFN type's own
// primary_identifier, so a scoped listing's returned identifiers can be
// checked positionally rather than assumed to be first. ok is false when
// scopeProperty is not part of the primary identifier at all - true for
// several of the 19 composite types this leg was built against
// (AWS::DataZone::Project requires "DomainIdentifier", a property distinct
// from either of its own primary_identifier entries, ["DomainId", "Id"]) -
// which this file treats as unverifiable rather than silently trusting the
// backend to have scoped correctly.
func scopePropertyIndex(primaryIdentifier []string, scopeProperty string) (idx int, ok bool) {
	for i, p := range primaryIdentifier {
		if p == scopeProperty {
			return i, true
		}
	}
	return 0, false
}

// identifierMatchesParent reports whether a Cloud Control identifier's
// scopeIndex'th "|"-joined segment equals parentValue, and the identifier
// has exactly arity segments - both have to hold for this result to be
// trusted as actually belonging to the parent it was scoped to. See
// [cloudcontrol.Client.ListResourcesScoped]'s own doc comment: floci's
// ListResources ignores ResourceModel scoping entirely and returns every
// live resource of the type, so this check - not the request this client
// sent - is what keeps a floci-backed run from misattributing one parent's
// children to another.
func identifierMatchesParent(identifier string, scopeIndex, arity int, parentValue string) bool {
	parts := strings.Split(identifier, "|")
	if arity > 0 && len(parts) != arity {
		return false
	}
	if scopeIndex >= len(parts) {
		return false
	}
	return parts[scopeIndex] == parentValue
}
