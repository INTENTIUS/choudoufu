// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package servicetags is GitHub issue #1131: the per-service tag-read leg,
// for a live object that an enumeration route reaches and no tag route can
// read a marker off.
//
// # The gap this closes
//
// Three routes can put an ownership marker in front of the sweep, and a
// type exists for which all three come back empty:
//
//  1. The provider's own list resource. Some list calls carry tags;
//     iam:ListRoles and iam:ListInstanceProfiles do not, and AWS's own
//     reference says so in as many words - "IAM resource-listing operations
//     return a subset of the available attributes for the resource. For
//     example, this operation does not return tags, even though they are an
//     attribute of the returned object."
//     (API_ListInstanceProfiles, quoted from the pinned SDK's own doc
//     comment in aws-sdk-go-v2/service/iam@v1.53.6).
//  2. Cloud Control's ListResources/GetResource. Cloud Control can only
//     return a property the CloudFormation schema declares, and
//     AWS::IAM::InstanceProfile declares no Tags property at all
//     (live/registry.json, tagging.taggable false). So GetResource on a
//     profile that demonstrably carries tofu-estate returns
//     {InstanceProfileName, Arn, Path} and nothing else. This is a fact
//     about the schema, not about any emulator.
//  3. The Resource Groups Tagging API's GetResources - internal/live/
//     discovery's tag index. #1134 measured a real account: it serves
//     iam:policy and iam:instance-profile in us-east-1 and never serves
//     iam:role anywhere. The pinned emulator serves no IAM at all
//     (lex00/floci#205, tracked as #1152).
//
// Where all three are silent, the object is enumerated and its ownership
// cannot be established, and #1129 made that a loud refusal
// (discovery.SweepGapMarkerUnreadable) rather than a silently dropped
// destroy. The marker is still there; the fourth route is the service's own
// tag API, and iam:ListInstanceProfileTags returns it.
//
// # What this package is, and is not
//
// It is one interface, [Reader], and one implementation of it, [IAM]. It is
// deliberately not a framework: a service is wired here when a type the
// sweep actually reaches needs it, and [IAMRoutes] is two entries because
// two types need it. See iam.go's own comment for the derivation, and
// iam_routes_test.go, which recomputes that derivation from the committed
// artifacts rather than trusting the list.
//
// # What it costs, and why that is not hidden
//
// One call per candidate object, not one per type. There is no batch tag
// read in IAM: ListInstanceProfiles omits tags by design (quoted above),
// GetInstanceProfile and ListInstanceProfileTags are both per-object, and
// no filter narrows either to a tag value. #1037 and #1039 made the native
// sweep flat in estate size and that flatness is a published claim
// (live/costs/plan-cost.md); this leg does not preserve it for
// the types it covers, and the call sites in internal/live/discovery are
// gated so that it runs only where the run has already established, on this
// target, that nothing else can answer. The gate is
// discovery.serviceTagRead's, not this package's.
package servicetags
