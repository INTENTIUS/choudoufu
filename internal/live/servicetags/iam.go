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
}

// IAMRoutes is which resource types this reader can answer for, and it is
// not a list of the types IAM CAN tag-read: it is the intersection of that
// with the types the sweep can reach and cannot otherwise read, which is
// two.
//
// The derivation, recomputed from the committed artifacts by
// TestIAMRoutesMatchTheDerivedSet:
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
// Eighteen types satisfy those three. Two of them are IAM's, and IAM is the
// service wired here because it is the one #1134 measured the Resource
// Groups Tagging API failing to cover and the one the pinned emulator
// serves a tag-read operation for. The other sixteen sit in services the
// tagging index does cover, so the gate in internal/live/discovery keeps
// this leg off for them; if one ever proves otherwise, it gets its own
// service wired here and its own entry in the table, not a general
// mechanism written in advance of a need.
// TestDerivedSetBeyondIAMIsNamedNotSilent names those sixteen.
//
// aws_iam_role and aws_iam_policy are deliberately absent even though
// #1134's real-AWS probe found the tagging index never serves iam:role.
// Both have a native provider list resource, so they never reach the Cloud
// Control leg at all - they reach internal/live/discovery's scanType, whose
// own marker gap (#1136, SweepGapMarkerUnreadable via sweepMarkerReadGap)
// this leg is not wired into. Wiring it there is the follow-up #1131 names
// and this unit does not do.
var IAMRoutes = map[string]iamTagOp{
	// iam:ListInstanceProfileTags. The import identity of an
	// aws_iam_instance_profile is the profile name, which is exactly what
	// InstanceProfileName wants.
	"aws_iam_instance_profile": func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListInstanceProfileTags(ctx, &iam.ListInstanceProfileTagsInput{
			InstanceProfileName: aws.String(importID),
			Marker:              markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	},

	// iam:ListMFADeviceTags. The import identity of an
	// aws_iam_virtual_mfa_device is its ARN, and IAM's own reference says
	// "for virtual MFA devices, the serial number is the same as the ARN",
	// so the identifier goes through unchanged here too.
	"aws_iam_virtual_mfa_device": func(ctx context.Context, api IAMAPI, importID, marker string) ([]iamtypes.Tag, *string, bool, error) {
		out, err := api.ListMFADeviceTags(ctx, &iam.ListMFADeviceTagsInput{
			SerialNumber: aws.String(importID),
			Marker:       markerOrNil(marker),
		})
		if err != nil {
			return nil, nil, false, err
		}
		return out.Tags, out.Marker, out.IsTruncated, nil
	},
}

// iamTagOp is one entry of [IAMRoutes]: a paginated tag read, returning the
// page's tags plus the continuation the next call needs.
type iamTagOp func(ctx context.Context, api IAMAPI, importID, marker string) (tags []iamtypes.Tag, next *string, truncated bool, err error)

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

// ReadTags implements [Reader].
//
// Pagination is honoured even though IAM caps a resource at 50 tags and the
// default page is 100, so a second page cannot arise today. It costs one
// comparison and it means the function is correct if that cap ever moves,
// which is cheaper than a comment explaining why it is safe not to.
func (r *IAM) ReadTags(ctx context.Context, typeName, importID string) (map[string]string, error) {
	op, ok := IAMRoutes[typeName]
	if !ok {
		return nil, noRouteError{typeName: typeName}
	}
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
