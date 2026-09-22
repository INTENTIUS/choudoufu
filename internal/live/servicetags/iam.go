// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// IAMAPI is the slice of aws-sdk-go-v2's IAM client this package calls, so
// that a test can stand in for it without an HTTP server. *iam.Client
// satisfies it.
//
// It holds exactly the operations [IAMRoutes] names. Widening it means
// widening that table, and iam_routes_test.go is what says whether the
// table is still the right one.
type IAMAPI interface {
	ListInstanceProfileTags(ctx context.Context, params *iam.ListInstanceProfileTagsInput, optFns ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error)
	ListMFADeviceTags(ctx context.Context, params *iam.ListMFADeviceTagsInput, optFns ...func(*iam.Options)) (*iam.ListMFADeviceTagsOutput, error)
	ListPolicyTags(ctx context.Context, params *iam.ListPolicyTagsInput, optFns ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error)
	ListRoleTags(ctx context.Context, params *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error)
	ListUserTags(ctx context.Context, params *iam.ListUserTagsInput, optFns ...func(*iam.Options)) (*iam.ListUserTagsOutput, error)
}

// IAMRoutes is which resource types this reader can answer for, and it is
// not a list of the types IAM CAN tag-read: it is the intersection of that
// with the types a sweep leg can reach and cannot otherwise read, which is
// five.
//
// The derivation, recomputed from the committed artifacts by
// TestIAMRoutesMatchTheDerivedSet. There are two arms, one per enumeration
// leg, and they are disjoint because a type either has a native list
// resource or it does not.
//
// The Cloud Control arm (#1131, the original two):
//
//   - the type is mapped to a CloudFormation type (live/mapping.json) whose
//     list handler needs no input (live/registry.json), so Cloud Control
//     enumerates it and internal/live/discovery's scanTypeCloudControl is
//     the leg that sees it;
//   - live/registry.json says that CFN type is NOT taggable, so neither
//     ListResources nor GetResource can ever return a marker for it;
//   - live/survey-full.json says the provider type IS taggable, so
//     internal/live/stamp writes a marker onto it and there is a marker
//     there to miss.
//
// The native arm (#1125, the three added here):
//
//   - live/survey-full.json says the provider type IS taggable, same clause
//     as above and for the same reason: a marker was written, so there is
//     one to miss;
//   - live/survey-full.json says the type HAS a list resource, so
//     internal/live/discovery's scanType is the leg that sees it and the
//     Cloud Control arm above never applies to it;
//   - the type is IAM's, which is
//     internal/live/discovery.TaggingAPIUnservedType's whole content today:
//     the Resource Groups Tagging API is not a fallback for it, so #266's
//     tag-index join cannot supply the marker the list call dropped.
//
// The third clause is why the native arm is written as "IAM" rather than as
// a general rule. It has to be read from the routing preference that sends
// IAM away from the tagging leg, and that predicate lives in
// internal/live/discovery, which imports this package. The test spells the
// clause out as the aws_iam_ prefix rather than importing it back.
//
// What makes the native arm a real gap rather than a hypothetical one is
// IAM's own API reference, which states it on each list operation. Verbatim,
// from botocore 1.43.70's iam/2010-05-08 model - ListRoles: "IAM
// resource-listing operations return a subset of the available attributes
// for the resource. This operation does not return the following attributes,
// even though they are an attribute of the returned object: PermissionsBoundary,
// RoleLastUsed, Tags". ListUsers says the same with PermissionsBoundary and
// Tags; ListPolicies and ListInstanceProfiles say "this operation does not
// return tags, even though they are an attribute of the returned object".
// So for all three native-arm types the provider's list resource is reading
// an API that drops tags by design, which is the same permanent fact the
// Cloud Control arm's missing Tags property is.
//
// Five types satisfy one arm or the other, and all five are IAM's. IAM is
// the service wired here because it is the one #1134 measured the Resource
// Groups Tagging API failing to cover and the one the pinned emulator
// serves a tag-read operation for. Sixteen more types satisfy the Cloud
// Control arm in services the tagging index does cover, and they have no
// route here, so the leg never runs for them; if the index proves not to
// cover one, it gets its own service wired here and its own entry in the
// table, not a general mechanism written in advance of a need.
// TestDerivedSetBeyondIAMIsNamedNotSilent names those sixteen.
var IAMRoutes = map[string]iamRoute{
	// iam:ListInstanceProfileTags. The import identity of an
	// aws_iam_instance_profile is the profile name, which is exactly what
	// InstanceProfileName wants.
	"aws_iam_instance_profile": {action: "iam:ListInstanceProfileTags", read: func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListInstanceProfileTags(ctx, &iam.ListInstanceProfileTagsInput{
			InstanceProfileName: aws.String(importID),
			Marker:              markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	}},

	// iam:ListMFADeviceTags. The import identity of an
	// aws_iam_virtual_mfa_device is its ARN, and IAM's own reference says
	// "for virtual MFA devices, the serial number is the same as the ARN",
	// so the identifier goes through unchanged here too.
	"aws_iam_virtual_mfa_device": {action: "iam:ListMFADeviceTags", read: func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListMFADeviceTags(ctx, &iam.ListMFADeviceTagsInput{
			SerialNumber: aws.String(importID),
			Marker:       markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	}},

	// iam:ListPolicyTags. The identifier discovery's importIdentity hands
	// this leg is the first of the identity table's IdentityAttrs the
	// listed object carries, and aws_iam_policy's are ["arn", "id"] - the
	// provider's own list identity schema carries arn and nothing else
	// (discovery.go's #1054 comment, measured with TF_LOG=debug), and the
	// provider's id for a managed policy is that same ARN. Either way the
	// string is an ARN, which is what PolicyArn wants.
	"aws_iam_policy": {action: "iam:ListPolicyTags", read: func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListPolicyTags(ctx, &iam.ListPolicyTagsInput{
			PolicyArn: aws.String(importID),
			Marker:    markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	}},

	// iam:ListRoleTags. aws_iam_role's IdentityAttrs are ["id", "name"] and
	// the provider sets a role's id to its name, so the identifier is the
	// role name whichever of the two the listed object carried - which is
	// what RoleName wants. live/survey-full.json agrees from the other
	// side: required_for_import ["name"].
	"aws_iam_role": {action: "iam:ListRoleTags", read: func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListRoleTags(ctx, &iam.ListRoleTagsInput{
			RoleName: aws.String(importID),
			Marker:   markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	}},

	// iam:ListUserTags. Same shape as the role above: IdentityAttrs
	// ["id", "name"], the provider sets a user's id to its name, and
	// UserName wants that name.
	"aws_iam_user": {action: "iam:ListUserTags", read: func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListUserTags(ctx, &iam.ListUserTagsInput{
			UserName: aws.String(importID),
			Marker:   markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	}},
}

// iamTagOp is the read half of an [IAMRoutes] entry: a paginated tag read,
// returning the page's tags plus the continuation the next call needs.
type iamTagOp func(ctx context.Context, api IAMAPI, importID, marker string) (tags []iamtypes.Tag, next *string, truncated bool, err error)

// iamRoute is one entry of [IAMRoutes]: the IAM action the read needs, in
// the form a policy statement names it, and the read itself. The action is
// what [IAM.Action] answers and what a refused read's gap tells the operator
// to grant (GitHub issue #1162's third MARKER_UNREADABLE sentence), so it is
// written once here beside the call that needs it and nowhere else.
type iamRoute struct {
	action string
	read   iamTagOp
}

// IAM is the [Reader] for AWS Identity and Access Management.
type IAM struct {
	api IAMAPI
}

// NewIAM builds the IAM reader over an already-configured client. A nil api
// is not special-cased: the caller decides whether to build a reader at
// all, and a reader that exists is one that can call.
func NewIAM(api IAMAPI) *IAM { return &IAM{api: api} }

// Route implements [Reader].
func (r *IAM) Route(typeName string) bool {
	_, ok := IAMRoutes[typeName]
	return ok
}

// Action implements [Reader].
func (r *IAM) Action(typeName string) string {
	return IAMRoutes[typeName].action
}

// ReadTags implements [Reader].
//
// Pagination is honoured even though IAM caps a resource at 50 tags and the
// default page is 100, so a second page cannot arise today. It costs one
// comparison and it means the function is correct if that cap ever moves,
// which is cheaper than a comment explaining why it is safe not to.
func (r *IAM) ReadTags(ctx context.Context, typeName, importID string) (map[string]string, error) {
	route, ok := IAMRoutes[typeName]
	if !ok {
		return nil, noRouteError{typeName: typeName}
	}
	op := route.read
	if importID == "" {
		return nil, fmt.Errorf("no identifier for a %s to read tags for", typeName)
	}

	tags := map[string]string{}
	var marker string
	for {
		page, next, truncated, err := op(ctx, r.api, importID, marker)
		if err != nil {
			return nil, err
		}
		for _, t := range page {
			if t.Key == nil {
				continue
			}
			tags[*t.Key] = aws.ToString(t.Value)
		}
		if !truncated || next == nil || *next == "" || *next == marker {
			return tags, nil
		}
		marker = *next
	}
}

func markerOrNil(marker string) *string {
	if marker == "" {
		return nil
	}
	return aws.String(marker)
}
