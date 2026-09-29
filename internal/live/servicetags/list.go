// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// GitHub issue #1477: the per-service LIST leg, one step before the tag
// read this package was written for.
//
// # The gap this closes
//
// aws_iam_service_linked_role is admitted, taggable, stamped with this
// estate's markers by internal/live/stamp, and since the 2026-09-11
// emulator repin (#1045, floci matching real AWS by no longer serving IAM
// through GetResources) it could not be recovered by any enumeration route
// internal/live/discovery had:
//
//   - the provider offers no list resource for it, and the aws_iam_role
//     list resource - whose iam:ListRoles call DOES return service-linked
//     roles - filters them out before discovery sees them, so #302's
//     sibling bind ([iamServiceLinkedRoleSibling] in that package) lost its
//     producer;
//   - AWS::IAM::ServiceLinkedRole has no Cloud Control list handler
//     (live/registry.json, handlers.list false);
//   - the Resource Groups Tagging API does not index IAM roles anywhere
//     (#1134), so the estate's tag index never holds one.
//
// The plan then proposed creating a role that exists and carries the
// estate's marker, and a second CreateServiceLinkedRole for a service that
// already has one fails on real AWS. The service's own API does list them:
// iam:ListRoles with PathPrefix=/aws-service-role/ returns every
// service-linked role in the account, with its Arn and RoleName, and
// iam:ListRoleTags reads its marker by name - the read [IAMRoutes] already
// carries for ordinary roles.
//
// # What a Lister is, and is not
//
// It is the enumeration half of the same shape [Reader] is the tag-read
// half of: a route table keyed by resource type, consulted before anything
// is paid for, answering for the types a human has read the service's API
// reference for and no other. It is not a general "list anything through
// the SDK" mechanism, and [IAMListRoutes] is one entry because one type
// needs it; TestIAMListRoutesMatchTheDerivedSet recomputes that membership
// from the committed artifacts so the table cannot silently be either
// wider or narrower than the gap.

// Lister enumerates one resource type's live objects through the service's
// own list API, for a type with no provider list resource and no Cloud
// Control list handler.
type Lister interface {
	// ListRoute reports whether this lister has a list operation for
	// typeName. A caller asks before it pays for anything else, so that a
	// type with no route costs nothing at all.
	ListRoute(typeName string) bool

	// ListAction is the IAM action name [List] needs for typeName, in the
	// form a policy statement names it ("iam:ListRoles"), and "" for a
	// type [ListRoute] answers false for. A failed listing's diagnostic
	// names it as the thing to grant.
	ListAction(typeName string) string

	// List returns every live object of typeName the service reports,
	// across every page. An error means nothing was established, and the
	// caller must keep whatever refusal it already had: a listing that
	// failed is not an empty account.
	List(ctx context.Context, typeName string) ([]Listed, error)
}

// Listed is one object a [Lister] enumerated. It carries the two
// identifiers the object has, because for the one type wired today they
// differ: a service-linked role imports by its ARN and reads its tags by
// its name.
type Listed struct {
	// ImportID is the object's identity in the resource type's own import
	// scheme - the string internal/live/discovery binds a declared
	// instance to and internal/live/projection later imports by.
	ImportID string

	// IdentityAttr is which of the type's identity attributes ImportID is
	// ("arn" for a service-linked role), for the binding record.
	IdentityAttr string

	// ReadKey is the identifier [Reader.ReadTags] takes for this object
	// under the same typeName. It equals ImportID for a type whose tag-read
	// operation keys on the import identity, and differs where the
	// service's tag API wants something else: iam:ListRoleTags takes
	// RoleName, and a service-linked role's import identity is its ARN.
	ReadKey string

	// Tags is whatever tag set the LISTING itself carried, and nil when the
	// list operation returns none. IAM's list operations document that
	// they drop tags ("this operation does not return the following
	// attributes, even though they are an attribute of the returned
	// object: PermissionsBoundary, RoleLastUsed, Tags" - ListRoles), so it
	// is empty on real AWS; a target that does populate it saves the
	// caller a tag read, which is the per-object gate's second clause.
	Tags map[string]string
}

// serviceLinkedRolePathPrefix is the path IAM reserves for its own
// service-linked roles. IAM prepends it on CreateServiceLinkedRole and
// refuses to let CreateRole use it, so a role under it is a service-linked
// role by construction and one outside it never is - the same ARN grammar
// internal/live/discovery's iamRoleEntry tells the two CFN types apart by.
const serviceLinkedRolePathPrefix = "/aws-service-role/"

// IAMListRoutes is which resource types [IAM] can enumerate, and it is not
// a list of the types IAM CAN list: it is the types no other enumeration
// route in internal/live/discovery reaches and whose marker this package
// can then read, which is one.
//
// The derivation, recomputed from the committed artifacts by
// TestIAMListRoutesMatchTheDerivedSet, is the third arm beside [IAMRoutes]'s
// two, and it is disjoint from both by construction:
//
//   - live/survey-full.json says the provider type IS taggable, so
//     internal/live/stamp writes a marker onto it and there is a marker to
//     find;
//   - live/survey-full.json says the type has NO list resource, so
//     internal/live/discovery's scanType has no native listing for it -
//     which is what keeps it out of the native arm;
//   - live/mapping.json and live/registry.json say its CFN type has no
//     input-free list handler, so Cloud Control cannot enumerate it either,
//     which is what keeps it out of the Cloud Control arm;
//   - the type is IAM's, the one service the Resource Groups Tagging API
//     does not index for roles (#1134), so #266's tag-index fallback for a
//     type with no list route ([scanTypeMarkerFallback] over there) finds
//     nothing either.
//
// One type satisfies all four at provider 6.59.0. The hand half of the
// entry is the same kind of fact as [IAMRoutes]'s: WHICH IAM operation
// enumerates the type and WHICH of its inputs narrows the listing to it.
// ListRoles takes PathPrefix, and AWS's own reference for
// CreateServiceLinkedRole says the role is created under
// /aws-service-role/SERVICE-NAME/; no schema this repository holds records
// either.
var IAMListRoutes = map[string]iamListRoute{
	// iam:ListRoles, PathPrefix=/aws-service-role/. Paginated with
	// Marker/IsTruncated exactly as the tag reads are; the pinned emulator
	// paginates ListRoles too (lex00/floci#202). The listed Role carries
	// Arn - the type's documented import identity, and the first of its
	// ratified IdentityAttrs ["arn", "id"] - and RoleName, which is what
	// iam:ListRoleTags wants, so both go into the [Listed].
	//
	// The path is checked client-side as well as sent server-side. A
	// target that ignored PathPrefix would otherwise hand every role in
	// the account to a leg that pays one tag read per object and binds
	// under a type whose import scheme an ordinary role does not share.
	"aws_iam_service_linked_role": {action: "iam:ListRoles", list: func(ctx context.Context, api IAMAPI) ([]Listed, error) {
		var out []Listed
		var marker string
		for {
			page, err := api.ListRoles(ctx, &iam.ListRolesInput{
				PathPrefix: aws.String(serviceLinkedRolePathPrefix),
				Marker:     markerOrNil(marker),
			})
			if err != nil {
				return nil, err
			}
			for _, r := range page.Roles {
				if !strings.HasPrefix(aws.ToString(r.Path), serviceLinkedRolePathPrefix) {
					continue
				}
				arn, name := aws.ToString(r.Arn), aws.ToString(r.RoleName)
				if arn == "" || name == "" {
					// A role with no ARN or no name is not something an
					// identity can be composed for, and IAM does not
					// return one. Skipped rather than bound to an empty
					// string.
					continue
				}
				var tags map[string]string
				for _, t := range r.Tags {
					if t.Key == nil {
						continue
					}
					if tags == nil {
						tags = map[string]string{}
					}
					tags[*t.Key] = aws.ToString(t.Value)
				}
				out = append(out, Listed{ImportID: arn, IdentityAttr: "arn", ReadKey: name, Tags: tags})
			}
			next := aws.ToString(page.Marker)
			if !page.IsTruncated || next == "" || next == marker {
				return out, nil
			}
			marker = next
		}
	}},
}

// iamListOp is the enumeration half of an [IAMListRoutes] entry: every
// page of the service's own listing, folded into [Listed] values.
type iamListOp func(ctx context.Context, api IAMAPI) ([]Listed, error)

// iamListRoute is one entry of [IAMListRoutes]: the IAM action the listing
// needs, in the form a policy statement names it, and the listing itself.
type iamListRoute struct {
	action string
	list   iamListOp
}

// ListRoute implements [Lister].
func (r *IAM) ListRoute(typeName string) bool {
	_, ok := IAMListRoutes[typeName]
	return ok
}

// ListAction implements [Lister].
func (r *IAM) ListAction(typeName string) string {
	return IAMListRoutes[typeName].action
}

// List implements [Lister].
func (r *IAM) List(ctx context.Context, typeName string) ([]Listed, error) {
	route, ok := IAMListRoutes[typeName]
	if !ok {
		return nil, noRouteError{typeName: typeName}
	}
	return route.list(ctx, r.api)
}
