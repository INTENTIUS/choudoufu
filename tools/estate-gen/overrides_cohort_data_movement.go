// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesDataMovement is the data-movement cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesDataMovement = map[string]typeOverride{
	"aws_appintegrations_data_integration": {
		Reasons: []string{
			`source_uri is a required string the schema does not constrain, but the provider validates it against a fixed pattern (validate: "invalid value for source_uri (should be a valid source uri)"), documented as a connector-profile scheme like "Salesforce://AppFlow/example"; the generic placeholder string does not match it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("source_uri", exprTokens(fmt.Sprintf(`"Salesforce://AppFlow/tofu-%s-cohort"`, g.cohort)))
		},
	},
	"aws_appintegrations_event_integration": {
		Reasons: []string{
			`event_filter.source is a required string the schema does not constrain, but the provider validates it against a fixed prefix regex (validate: "should be not be more than 255 alphanumeric, forward slashes, dots, underscores, or hyphen characters" - the message text does not match the actual pattern, which the provider's own source requires to start "aws.partner/"); the generic placeholder string does not start with it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "event_filter" {
					blk.Body().SetAttributeRaw("source", exprTokens(fmt.Sprintf(
						`"aws.partner/tofu-%s-cohort"`, g.cohort)))
				}
			}
		},
	},
	"aws_datasync_agent": {
		Reasons: []string{
			`activation_key and ip_address are both Optional in the schema, but the provider requires one of them at apply time ("one of activation_key or ip_address is required") - an apply-time-only gap ` + "`terraform validate`" + ` does not catch, found by hand-verifying this cohort against the pinned floci image. ip_address makes Terraform itself perform an HTTP GET against that address to retrieve the real activation key before the DataSync API call happens at all, which hangs indefinitely against any address that is not an actual reachable agent appliance; activation_key needs no such round-trip, so it is the one this override sets`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("activation_key", exprTokens(`"placeholder-activation-key"`))
		},
	},
	"aws_datasync_location_azure_blob": {
		Reasons: []string{
			`authentication_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected authentication_type to be one of [\"SAS\" \"NONE\"]"); agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authentication_type", exprTokens(`"SAS"`))
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_efs": {
		Reasons: []string{
			`efs_file_system_arn and ec2_config's security_group_arns/subnet_arn are all required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("efs_file_system_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:elasticfilesystem:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "ec2_config" {
					blk.Body().SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
					blk.Body().SetAttributeRaw("subnet_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:ec2:us-east-1:000000000000:subnet/subnet-tofu%scohort"`, g.cohort)))
				}
			}
		},
	},
	"aws_datasync_location_fsx_lustre_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_fsx_ontap_file_system": {
		Reasons: []string{
			`security_group_arns and storage_virtual_machine_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); protocol's nfs and smb sub-blocks are both Optional in the schema, but the provider requires exactly one set (validate: "one of protocol.0.nfs,protocol.0.smb must be specified"), and the generic pass renders neither, unlike its sibling aws_datasync_location_fsx_openzfs_file_system, whose protocol block has only one sub-block to choose`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
			body.SetAttributeRaw("storage_virtual_machine_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:storage-virtual-machine/svm-tofu%scohort"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "protocol" {
					nfs := blk.Body().AppendNewBlock("nfs", nil)
					nfs.Body().AppendNewBlock("mount_options", nil)
				}
			}
		},
	},
	"aws_datasync_location_fsx_openzfs_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_fsx_windows_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_hdfs": {
		Reasons: []string{
			`agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); name_node.port is a required number the schema does not range-constrain, but the provider validates it is a valid port (validate: "expected \"name_node.0.port\" to be a valid port number, got: 0"), and the generic pass's numeric zero placeholder is not one; authentication_type is Optional in the schema, but the provider's own AWS SDK request validation requires it client-side before any HTTP call is made ("missing required field, CreateLocationHdfsInput.AuthenticationType") - ` + "`terraform validate`" + ` does not catch this one either, only an apply against the pinned floci image did; SIMPLE also requires simple_user, which the schema likewise leaves Optional`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
			body.SetAttributeRaw("authentication_type", exprTokens(`"SIMPLE"`))
			body.SetAttributeRaw("simple_user", exprTokens(`"placeholder"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "name_node" {
					blk.Body().SetAttributeRaw("port", exprTokens(`8020`))
				}
			}
		},
	},
	"aws_datasync_location_nfs": {
		Reasons: []string{
			`on_prem_config.agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "on_prem_config" {
					blk.Body().SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
				}
			}
		},
	},
	"aws_datasync_location_s3": {
		Reasons: []string{
			`s3_bucket_arn is a required string the provider validates is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("s3_bucket_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:s3:::tofu-%s-cohort-datasync-s3"`, g.cohort)))
		},
	},
	"aws_datasync_location_smb": {
		Reasons: []string{
			`agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_task": {
		Reasons: []string{
			`destination_location_arn and source_location_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_datasync_location_nfs.app.arn and aws_datasync_location_s3.app.arn, the cross-resource reference issue #56 asks for and the provider's own documented example uses verbatim`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if src, ok := g.byType["aws_datasync_location_nfs"]; ok {
				body.SetAttributeRaw("source_location_arn", exprTokens(fmt.Sprintf("%s.arn", src)))
			}
			if dst, ok := g.byType["aws_datasync_location_s3"]; ok {
				body.SetAttributeRaw("destination_location_arn", exprTokens(fmt.Sprintf("%s.arn", dst)))
			}
		},
	},
	"aws_dms_certificate": {
		Reasons: []string{
			`certificate_pem and certificate_wallet are both Optional in the schema, but the provider requires exactly one set (validate: "one of certificate_pem,certificate_wallet must be specified"), and the generic pass sets neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("certificate_pem", exprTokens(`"placeholder-pem"`))
		},
	},
	"aws_dms_endpoint": {
		Reasons: []string{
			`endpoint_type and engine_name are both required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); "s3" is not among engine_name's valid values - that shape is what the separate aws_dms_s3_endpoint type below covers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("endpoint_type", exprTokens(`"source"`))
			body.SetAttributeRaw("engine_name", exprTokens(`"mysql"`))
		},
	},
	"aws_dms_event_subscription": {
		Reasons: []string{
			`sns_topic_arn is a required string the provider validates is a well-formed ARN (validate: "is an invalid ARN"), and no aws_sns_topic is part of this cohort to reference, the same gap aws_db_event_subscription's own override fills; source_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected source_type to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sns_topic_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-events"`, g.cohort)))
			body.SetAttributeRaw("source_type", exprTokens(`"replication-task"`))
		},
	},
	"aws_dms_replication_config": {
		Reasons: []string{
			`replication_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected replication_type to be one of [...]"); source_endpoint_arn and target_endpoint_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_dms_endpoint.app.endpoint_arn and aws_dms_s3_endpoint.app.endpoint_arn, the cross-resource reference issue #56 asks for and the provider's own documented example uses the same way; table_mappings is a required string the provider validates is well-formed JSON (validate: "contains an invalid JSON"); compute_config.replication_subnet_group_id is Optional in the schema, but the generic pass already renders a placeholder for it - overridden to this cohort's own aws_dms_replication_subnet_group.app.replication_subnet_group_id instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("replication_type", exprTokens(`"full-load"`))
			if ep, ok := g.byType["aws_dms_endpoint"]; ok {
				body.SetAttributeRaw("source_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			if ep, ok := g.byType["aws_dms_s3_endpoint"]; ok {
				body.SetAttributeRaw("target_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			body.SetAttributeRaw("table_mappings", exprTokens(`jsonencode({
    rules = [{
      rule-type   = "selection"
      rule-id     = "1"
      rule-name   = "1"
      object-locator = {
        schema-name = "%"
        table-name  = "%"
      }
      rule-action = "include"
    }]
  })`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "compute_config" {
					if sng, ok := g.byType["aws_dms_replication_subnet_group"]; ok {
						blk.Body().SetAttributeRaw("replication_subnet_group_id", exprTokens(
							fmt.Sprintf("%s.replication_subnet_group_id", sng)))
					}
				}
			}
		},
	},
	"aws_dms_replication_subnet_group": {
		Reasons: []string{
			`subnet_ids is a required list with a provider-enforced 2-item minimum (validate: "Attribute subnet_ids requires 2 item minimum, but config has only 1 declared"), the same MinItems gap aws_route53_resolver_firewall_rule's ip_address override fixes in the observability batch, and the schema itself does not say so`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("subnet_ids", exprTokens(
				`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
		},
	},
	"aws_dms_replication_task": {
		Reasons: []string{
			`migration_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected migration_type to be one of [...]"); replication_instance_arn, source_endpoint_arn and target_endpoint_arn are all required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_dms_replication_instance.app.replication_instance_arn, aws_dms_endpoint.app.endpoint_arn and aws_dms_s3_endpoint.app.endpoint_arn, the cross-resource reference issue #56 asks for; table_mappings is a required string the provider validates is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("migration_type", exprTokens(`"full-load"`))
			if ri, ok := g.byType["aws_dms_replication_instance"]; ok {
				body.SetAttributeRaw("replication_instance_arn", exprTokens(
					fmt.Sprintf("%s.replication_instance_arn", ri)))
			}
			if ep, ok := g.byType["aws_dms_endpoint"]; ok {
				body.SetAttributeRaw("source_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			if ep, ok := g.byType["aws_dms_s3_endpoint"]; ok {
				body.SetAttributeRaw("target_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			body.SetAttributeRaw("table_mappings", exprTokens(`jsonencode({
    rules = [{
      rule-type   = "selection"
      rule-id     = "1"
      rule-name   = "1"
      object-locator = {
        schema-name = "%"
        table-name  = "%"
      }
      rule-action = "include"
    }]
  })`))
		},
	},
	"aws_dms_s3_endpoint": {
		Reasons: []string{
			`endpoint_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected endpoint_type to be one of [...]") - set to "target", pairing with aws_dms_endpoint.app's own "source" so aws_dms_replication_config and aws_dms_replication_task above have one endpoint of each type to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("endpoint_type", exprTokens(`"target"`))
		},
	},
	"aws_transfer_user": {
		Reasons: []string{
			`server_id is a required string the provider validates is a well-formed Transfer server id, lowercase alphanumeric only (validate: "isn't a valid transfer server id") - the generic placeholder string is not one; overridden to this cohort's own aws_transfer_server.app.id, the cross-resource reference issue #56 asks for and exactly the composite this type's own internal/live/identity/table.go entry (server_id/user_name) is ratified on`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if srv, ok := g.byType["aws_transfer_server"]; ok {
				body.SetAttributeRaw("server_id", exprTokens(fmt.Sprintf("%s.id", srv)))
			}
		},
	},
	"aws_transfer_workflow": {
		Reasons: []string{
			`steps.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [...]"); DELETE needs no further delete_step_details block, the smaller of the five shapes`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "steps" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"DELETE"`))
				}
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesDataMovement) }
