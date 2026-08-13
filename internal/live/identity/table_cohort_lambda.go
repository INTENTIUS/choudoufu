// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableLambda is the lambda cohort's slice of [DefaultTable]:
// the identity rows the lambda ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableLambda = buildTable(
	// ---- Registry-ratified (#40, #44): first batch, Lambda -----------
	//
	// Every row below started as a tools/row-gen proposal built from
	// live/registry.json (the CloudFormation Registry's primaryIdentifier,
	// readOnlyProperties and createOnlyProperties for the type's mapped
	// CFN type) rather than from the provider's own identity schema. The
	// registry evidence is strong for whether an argument is
	// provider-assigned at all, and weak for whether the CFN type's own
	// notion of "identity" is the string this Terraform provider actually
	// imports by — CloudFormation and the provider model the same AWS API
	// independently, and the two "Reason" mismatches this batch found
	// (aws_lambda_alias, aws_lambda_layer_version_permission — recorded
	// below rather than in this table, precisely because they are not
	// here) were exactly that: the CFN type's own read-only field is not
	// what the provider's import syntax names. Every row that did land
	// here was cross-checked against the provider's documented import
	// section (or, where present, its own identity schema in
	// live/survey-full.json) rather than accepted on the registry's word
	// alone. Cohort estate: live/e2e/estates/lambda.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_lambda_alias: row-gen proposed server-assigned via the CFN
	//     registry's AliasArn (read-only in the registry). The provider
	//     disagrees: aws_lambda_alias has no identity schema in v6.58.0,
	//     and its documented import ID is "function_name/alias_name" — a
	//     composite of two arguments the configuration already sets, not
	//     an ARN the alias resource mints. AliasArn is CloudFormation's
	//     own read-only projection of those same two arguments, not a
	//     value this provider treats as an identity.
	//   - aws_lambda_layer_version_permission: row-gen proposed
	//     server-assigned via the registry's opaque "Id". The provider's
	//     documented import ID is "layer-arn,version-number" — again a
	//     composite the configuration already supplies (the layer's ARN
	//     and the version being granted permission on), not the
	//     registry's Id. Same failure shape as the alias above.
	//
	// Both would need a hand-chosen composite separator row-gen's own
	// rules already refuse to guess (issue #44); this batch chooses not to
	// hand-write one for either without a config-signal check first, so
	// both stay out rather than land half-verified.

	serverAssigned("aws_lambda_code_signing_config",
		"Lambda mints the code signing config's ARN at create time; the type has no name argument for a wrong guess to reach for.",
		"CODESIGNINGCONFIGARN", "arn", "id"),
	serverAssigned("aws_lambda_event_source_mapping",
		"Lambda mints the event source mapping's UUID at create time; the event_source_arn in configuration names what it reads from, not the mapping itself.",
		"UUID", "uuid", "id"),
	serverAssigned("aws_lambda_layer_version",
		"Lambda mints the layer version's ARN at create time, embedding a version number it assigns and increments itself; the layer_name argument names the family, not one immutable version of it.",
		"LAYERVERSIONARN", "arn", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[CapacityProviderName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required_for_import=[name]) and against
		// the documented import command, which sets id to the same name.
		Type:          "aws_lambda_capacity_provider",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[FunctionName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's own identity schema
		// (live/survey-full.json: required_for_import=[function_name]) and
		// against the documented legacy import command, which sets id to
		// the function name.
		Type:          "aws_lambda_function",
		Components:    []Component{attr("function_name")},
		ImportSyntax:  "FUNCTION_NAME",
		IdentityAttrs: []string{"id", "function_name"},
	},
)

func init() { registerCohortTable(identityTableLambda) }
