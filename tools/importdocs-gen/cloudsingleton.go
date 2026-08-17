// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "regexp"

// This file answers one question nothing else in this artifact could state:
// is the documented import ID, in its ENTIRETY, a property of the cloud the
// run is pointed at rather than anything the resource itself holds?
//
// The provider documents a population of per-account, per-region singleton
// settings resources whose import ID is the Region name and nothing else -
// "Since there is only one configuration per account and region, region is
// used as the resource ID" (aws_ec2_allowed_images_settings), "import App
// Runner default auto scaling configurations using the current Region"
// (aws_apprunner_default_auto_scaling_configuration_version), and the 1.12+
// identity block spelled `identity = { region = "us-west-2" }` on fifteen
// more. internal/live/identity spells that shape Component{Cloud: "region"}
// and resolves it from configuration alone, with no cloud read and no
// marker - but only if something in the artifact says the ID IS the region.
// Until this field nothing did: the Import section names no argument (so
// ComposedOfArguments is null), the example is neither ARN- nor URL-formed
// (so IDTemplate is nil), and the sole ID part, where the prose names one
// at all, resolves to an exported attribute.
//
// WHY THIS DOES NOT REUSE arnRegionRe, WHICH IS THE OBVIOUS THING TO DO.
// idtemplate.go's arnRegionRe (`^[a-z]{2}(-[a-z]+)+-\d+$`) is sound where
// it already runs - inside an ARN, at the fifth colon-separated position,
// where the POSITION carries the meaning and the regexp only has to
// recognise the value sitting in it. Over a whole import ID no position
// carries anything, and the same expression is then a claim about arbitrary
// text. Measured over the 1699-row 6.59.0 cache it makes that claim wrongly:
// aws_redshift_cluster's documented example is "tf-redshift-cluster-12345",
// which arnRegionRe matches in full. Tightening the trailing group to a
// single digit does not rescue it either - that admits
// aws_iam_instance_profile's "app-instance-profile-1" instead. No shape
// test over free text separates a region name from a hyphenated resource
// name, so this one matches AWS's own region VOCABULARY: a geography code,
// an optional partition segment, a compass direction and an ordinal. Both
// counterexamples fail at the geography code.
//
// WHY THE ACCOUNT HALF IS DELIBERATELY NOT RECORDED. The symmetric reading
// would answer "account-id" for an example that is a bare AWS account
// number, using idtemplate.go's arnAccountRe the same way, and it fails for
// the same reason with no vocabulary available to rescue it - an account ID
// is twelve arbitrary digits, so nothing distinguishes one from any other
// digit string. Three counterexamples in this same cache, each of which a
// digits-only test calls an account ID and none of which is the run's own
// account:
//
//   - aws_athena_named_query documents "import Athena Named Query using the
//     query ID", example "0123456789". The ID is the query's; the doc
//     merely spells its placeholder in digits.
//   - aws_cloudtrail_organization_delegated_admin_account and
//     aws_vpc_ipam_organization_admin_account both document "using the
//     delegate account `id`", example "12345678901" - a DIFFERENT account's
//     ID, supplied as a configuration argument, which is the opposite of
//     what identity.CloudAccountID means.
//
// So this field's vocabulary is the one value the evidence supports. It
// widens when a source that can tell those three apart from a real account
// singleton exists, and not before.

// cloudRegionValue is internal/live/identity.CloudRegion's own string, the
// spelling every consumer of this field matches against. It is duplicated
// here rather than imported for the reason parse.go's CloudDefault values
// are: this tool reads provider documentation and writes JSON, and taking a
// dependency on the identity package to name one constant would invert
// which side of the pipeline defines the vocabulary.
const cloudRegionValue = "region"

// soleRegionRe matches an AWS Region name and nothing else: a geography
// code, an optional partition segment for the isolated and GovCloud
// partitions, a compass direction, and the ordinal that distinguishes
// regions within a geography. Anchored whole-string, because a region name
// appearing as one SEGMENT of a larger ID is idtemplate.go's business and
// is already attributed there.
//
// The three vocabularies are closed sets AWS itself publishes, which is
// what makes this a vocabulary rather than a shape guess - and they are why
// "tf-redshift-cluster-12345" and "app-instance-profile-1", the two
// measured counterexamples this file's doc comment names, are refused at
// their first segment. A new geography code is a one-line addition here,
// the same standing templateFiller and candidateSeparators already have.
var soleRegionRe = regexp.MustCompile(`^(af|ap|ca|cn|eu|il|me|mx|sa|us)(-(gov|iso|isob|isoe|isof))?-(central|east|north|northeast|northwest|south|southeast|southwest|west)-\d$`)

// soleIDCloudValue reports the cloud property the documented import ID IS,
// in full, or "" for every other example - which is the overwhelming
// majority, including every composite and every ARN.
func soleIDCloudValue(idExample string) string {
	if soleRegionRe.MatchString(idExample) {
		return cloudRegionValue
	}
	return ""
}
