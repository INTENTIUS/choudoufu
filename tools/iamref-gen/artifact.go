// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// Artifact is live/iam-reference.json: for every AWS service the admission
// table reaches, whether the actions this fork performs on it can be scoped
// by the two tag condition keys marker governance depends on.
//
// It exists because live/MARKERS.md publishes an SCP and then says it cannot
// vouch for it - "that action list is illustrative, not exhaustive or
// verified for every admitted type [...] there is no generated artifact to
// check it against". This is that artifact (issue #152).
type Artifact struct {
	Source        string `json:"source"`
	GeneratedBy   string `json:"generated_by"`
	IndexModified int64  `json:"index_modified"`

	Counts Counts `json:"counts"`
	Rows   []Row  `json:"rows"`
}

// Counts are the roster-wide totals, and the honest ones: a service the
// reference does not cover, or an action it does not list, is counted rather
// than dropped.
type Counts struct {
	// Services is how many the admission table reaches and this artifact
	// therefore describes.
	Services int `json:"services"`

	// Resolved is how many of those matched exactly one IAM service name
	// the reference knows. Unresolved and Ambiguous are the two ways that
	// can fail, and both are failures of this pipeline rather than of the
	// reference.
	Resolved   int `json:"resolved"`
	Unresolved int `json:"unresolved"`
	Ambiguous  int `json:"ambiguous"`

	// The three ways a resolved service can still yield no verdict, kept
	// apart because they mean different things and one number would hide
	// all three. NoTagVerbRecorded is live/tag-verbs.json's own gap (the
	// service has no unambiguous tagging operation); TagActionAbsent is a
	// verb that artifact records and the reference does not list, which
	// means botocore and IAM name the operation differently and is worth
	// seeing.
	TagActionFound    int `json:"tag_action_found"`
	TagActionAbsent   int `json:"tag_action_absent_from_reference"`
	NoTagVerbRecorded int `json:"no_tag_verb_recorded"`

	// ServicesListingResourceTag is how many resolved services name
	// aws:ResourceTag on at least one action. It is a LOWER BOUND on how
	// many support it, not a measurement of how many do - see
	// Row.ActionsListingResourceTag for why, and do not publish its
	// complement as a count of services that lack support.
	ServicesListingResourceTag int `json:"services_listing_resource_tag"`

	// ListsResourceTag and ListsTagKeys count, OUT OF TagActionFound, the
	// services whose tagging verb names each condition key. Fractions of
	// what was checked, never of Services.
	//
	// The aws:TagKeys figure is the one live/MARKERS.md's SCP section can
	// lean on, and it is nearly universal. The aws:ResourceTag figure is a
	// lower bound in the same way the service-wide one is.
	ListsResourceTag int `json:"lists_resource_tag"`
	ListsTagKeys     int `json:"lists_tag_keys"`
}

// Row is one service.
type Row struct {
	// Service is the CloudFormation service segment, the key
	// live/tag-verbs.json and live/mapping.json both use.
	Service string `json:"service"`

	// IAMPrefix is the name IAM actions use, resolved from
	// live/tag-verbs.json's candidates against the reference's own index -
	// derived, never mapped by hand. Empty when nothing resolved.
	IAMPrefix string `json:"iam_prefix,omitempty"`

	// Candidates is what was tried, kept so an unresolved row is
	// diagnosable rather than merely absent.
	Candidates []string `json:"candidates,omitempty"`

	// TagAction is the service's own tagging operation, from
	// live/tag-verbs.json. TagActionFound says whether the reference lists
	// an action of that name.
	TagAction      string `json:"tag_action,omitempty"`
	TagActionFound bool   `json:"tag_action_found"`

	// ListsResourceTag and ListsTagKeys report whether the reference's entry
	// for the tagging action NAMES each condition key, at the action level
	// or on one of its resource types.
	//
	// "Lists", not "supports", and the distinction is load-bearing. This
	// document is authoritative about what it names and NOT about what it
	// omits: lambda:GetFunction carries a resource entry with no
	// ConditionKeys at all, while AWS's own IAM documentation describes
	// tag-based authorization for Lambda. So a true here is evidence a
	// policy condition will be evaluated; a false is the absence of a
	// statement, not a statement of absence.
	//
	// Anything rendered from these fields has to keep that asymmetry. A
	// roster of "services where the condition key works" can be built from
	// the trues. A roster of "services where it does not" cannot be built
	// from the falses.
	ListsResourceTag bool `json:"lists_resource_tag"`
	ListsTagKeys     bool `json:"lists_tag_keys"`

	// UntagAction is the service's own tag-REMOVAL operation, from
	// live/tag-verbs.json (ec2:DeleteTags, kms:UntagResource,
	// route53:ChangeTagsForResource). UntagActionFound says whether the
	// reference lists an action of that name, and UntagListsTagKeys whether
	// that action names aws:TagKeys - the condition an SCP denying marker
	// removal is written against.
	//
	// "Lists", again, and for the same reason: a true here is evidence the
	// Deny will be evaluated, a false is the absence of a statement rather
	// than a statement of absence. live/MARKERS.md's own warning is that a
	// policy which looks correct and silently does nothing is worse than no
	// policy, so this field must never be read as the second thing.
	UntagAction       string `json:"untag_action,omitempty"`
	UntagActionFound  bool   `json:"untag_action_found"`
	UntagListsTagKeys bool   `json:"untag_lists_tag_keys"`

	// ActionsTotal and ActionsListingResourceTag describe the whole service
	// rather than its tagging verb, because a grant policy conditioned on a
	// marker tag governs the ordinary actions - the describes, updates and
	// deletes - and a tagging call is the odd one out (what authorizes it is
	// usually aws:RequestTag, the tags being written, rather than
	// aws:ResourceTag, the tags already there; kms:TagResource lists exactly
	// that pair and no aws:ResourceTag).
	//
	// Read them with the same asymmetry as ListsResourceTag above, and with
	// more care, because at this scale the sparseness dominates: 142 of 160
	// resolved services list aws:ResourceTag on no action at all, and Lambda
	// - which does support tag-based authorization - is among them. These
	// counts measure what the reference states, and the reference does not
	// set out to enumerate every global condition key per action.
	//
	// They are here because the count of services that DO name it (18) is a
	// real lower bound and a usable starting roster for #142. The complement
	// is not a roster of anything.
	ActionsTotal              int `json:"actions_total,omitempty"`
	ActionsListingResourceTag int `json:"actions_listing_resource_tag,omitempty"`

	// Reason names why a row carries no verdict, when it does not.
	Reason string `json:"reason,omitempty"`
}
