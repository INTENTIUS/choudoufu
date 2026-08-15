// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesConnectEuc is the connect-euc cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesConnectEuc = map[string]typeOverride{
	// ---- data-movement batch (issue #65) ---------------------------------

	// Connect and end-user computing batch (issue #65). Every argument below
	// is Optional-shaped in the wire schema (a validation rule the schema
	// itself does not carry enforces it in practice) or a closed enum the
	// generic pass's "placeholder" string is never a member of - the same
	// two failure shapes issue #56 named for the Lambda and S3 cohorts.
	"aws_connect_hours_of_operation": {
		Reasons: []string{
			`config.day is Required and the provider validates it against a closed enum (validate: "expected day to be one of [SUNDAY MONDAY ...]"); the generic pass's "placeholder" string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "config" {
					blk.Body().SetAttributeRaw("day", exprTokens(`"MONDAY"`))
				}
			}
		},
	},
	"aws_connect_instance": {
		Reasons: []string{
			`directory_id and instance_alias are both Optional in the schema, but the provider requires exactly one (validate: "Missing required argument": "one of directory_id,instance_alias must be specified"); identity_management_type is Required and validated against a closed enum (validate: "expected identity_management_type to be one of [...]"), and the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_alias", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-connect"`, g.cohort)))
			body.SetAttributeRaw("identity_management_type", exprTokens(`"CONNECT_MANAGED"`))
		},
	},
	"aws_connect_quick_connect": {
		Reasons: []string{
			`quick_connect_config is a required block whose own quick_connect_type is Required and validated against a closed enum (validate: "expected quick_connect_type to be one of [...]"); the provider additionally requires exactly one of phone_config/queue_config/user_config depending on quick_connect_type (documented, not schema-Required) - PHONE_NUMBER chosen as the type needing the fewest nested fields, with phone_config.phone_number set to a well-formed E.164 number`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "quick_connect_config" {
					blk.Body().SetAttributeRaw("quick_connect_type", exprTokens(`"PHONE_NUMBER"`))
					phone := blk.Body().AppendNewBlock("phone_config", nil)
					phone.Body().SetAttributeRaw("phone_number", exprTokens(`"+12345550100"`))
				}
			}
		},
	},
	"aws_connect_routing_profile": {
		Reasons: []string{
			`media_concurrencies is a required block whose own channel is Required and validated against a closed enum (validate: "expected channel to be one of [...]"), and concurrency is Required and validated against the range 1-10 (validate: "expected media_concurrencies.0.concurrency to be in the range (1 - 10)") - the generic pass's zero value fails it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "media_concurrencies" {
					blk.Body().SetAttributeRaw("channel", exprTokens(`"VOICE"`))
					blk.Body().SetAttributeRaw("concurrency", exprTokens(`1`))
				}
			}
		},
	},
	"aws_connect_user": {
		Reasons: []string{
			`phone_config.phone_type is Required and validated against a closed enum (validate: "expected phone_type to be one of [SOFT_PHONE DESK_PHONE]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "phone_config" {
					blk.Body().SetAttributeRaw("phone_type", exprTokens(`"SOFT_PHONE"`))
				}
			}
		},
	},
	"aws_workspaces_pool": {
		Reasons: []string{
			`bundle_id and directory_id are both Required and the provider validates each against its own fixed prefix-and-length shape (validate: "Bundle ID must be in the format 'wsb-xxxxxxxx'", "Directory ID must be in the format 'wsd-xxxxxxxx'"), which the generic "placeholder" string satisfies neither; running_mode is Required and validated against a closed enum (validate: "expected running_mode to be one of [AUTO_STOP ALWAYS_ON]"); capacity is a required block (Argument Reference: "(Required) Capacity configuration") that is Optional-shaped in the wire schema, so terraform validate does not catch its absence - found only by exercising a create against floci during this batch's verification (operation error WorkSpaces: CreateWorkspacesPool, "missing required field, CreateWorkspacesPoolInput.Capacity")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("bundle_id", exprTokens(`"wsb-0123456789"`))
			body.SetAttributeRaw("directory_id", exprTokens(`"wsd-0123456789"`))
			body.SetAttributeRaw("running_mode", exprTokens(`"AUTO_STOP"`))
			capBlk := body.AppendNewBlock("capacity", nil)
			capBlk.Body().SetAttributeRaw("desired_user_sessions", exprTokens(`1`))
		},
	},
	"aws_workspacesweb_browser_settings": {
		Reasons: []string{
			`browser_policy is Required and the provider validates it is well-formed JSON (validate: "not valid JSON string format"); the generic placeholder string is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("browser_policy", exprTokens(`jsonencode({
    chromePolicies = {
      DefaultDownloadDirectory = {
        value = "/tmp"
      }
    }
  })`))
		},
	},
	"aws_workspacesweb_identity_provider": {
		Reasons: []string{
			`identity_provider_type is Required and validated against a closed enum (validate: "expected identity_provider_type to be one of [SAML Facebook Google LoginWithAmazon SignInWithApple OIDC]"); portal_arn is Required and validated as a well-formed ARN (validate: "cannot be parsed as an ARN"), wired to this cohort's own aws_workspacesweb_portal for a real one; identity_provider_details is a required map the generic pass leaves empty, which the schema alone does not reject but the provider needs SAML metadata in practice for a SAML provider`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("identity_provider_type", exprTokens(`"SAML"`))
			portalExpr := `"arn:aws:workspaces-web:us-east-1:000000000000:portal/tofucohortid00"`
			if portal, ok := g.byType["aws_workspacesweb_portal"]; ok {
				portalExpr = portal.String() + ".portal_arn"
			}
			body.SetAttributeRaw("portal_arn", exprTokens(portalExpr))
			body.SetAttributeRaw("identity_provider_details", exprTokens(fmt.Sprintf(`{
    MetadataURL = "https://example.com/tofu-%s-cohort-saml-metadata.xml"
  }`, g.cohort)))
		},
	},
	"aws_workspacesweb_network_settings": {
		Reasons: []string{
			`subnet_ids is Required and the provider validates it has between 2 and 5 elements (validate: "set must contain at least 2 elements and at most 5 elements, got: 1"); the generic pass's single-element placeholder list is one short`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0", "subnet-0fedcba9876543210"]`))
		},
	},
	"aws_workspacesweb_session_logger": {
		Reasons: []string{
			`event_filter and log_configuration are both optional-shaped in the schema (MinItems 0) but the provider requires both in practice (validate: "Block event_filter/log_configuration must have a configuration value as the provider has marked it as required"); event_filter needs exactly one of all/include (documented, not schema-Required - all{} chosen as the simpler member), and log_configuration.s3 needs bucket, folder_structure and log_file_format, all three Required once the s3 block itself is reached. folder_structure is "Flat", not the provider's own Argument Reference prose ("FlatStructure") - the real validator disagrees with its own docs text (validate: "Valid Values: [Flat NestedByDate]"), found only by running this override through terraform validate, the same doc/validator mismatch this batch's ratification work is built to catch`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ef := body.AppendNewBlock("event_filter", nil)
			ef.Body().AppendNewBlock("all", nil)

			lc := body.AppendNewBlock("log_configuration", nil)
			s3 := lc.Body().AppendNewBlock("s3", nil)
			s3.Body().SetAttributeRaw("bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-session-logs"`, g.cohort)))
			s3.Body().SetAttributeRaw("folder_structure", exprTokens(`"Flat"`))
			s3.Body().SetAttributeRaw("log_file_format", exprTokens(`"Json"`))
		},
	},
	"aws_workspacesweb_user_access_logging_settings": {
		Reasons: []string{
			`kinesis_stream_arn is Required and validated as a well-formed ARN (validate: "cannot be parsed as an ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("kinesis_stream_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kinesis:us-east-1:000000000000:stream/tofu-%s-cohort-user-access-logs"`, g.cohort)))
		},
	},
	// WorkSpacesWeb's eight *_association fold-children (issue #68's
	// fold-child admission path merged mid-batch; see the batch banner
	// comment in internal/live/identity/table.go for why none of the
	// eight actually needs that path's own machinery). Both arguments are
	// ARNs of server-assigned siblings, so gen.go's identityArgName-based
	// auto-wiring never fires (the same reason aws_eip_association's own
	// instance_id above needs an explicit override) - each is wired here
	// to the real sibling this cohort renders, or a literal placeholder
	// ARN otherwise.
}

func init() { registerCohortOverrides(typeOverridesConnectEuc) }
