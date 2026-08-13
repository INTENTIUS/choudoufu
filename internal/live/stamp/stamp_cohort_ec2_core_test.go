// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The ec2-core cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableEc2Core = []string{
	// Registry-ratified EC2 core batch (#40, #44, issue #65). See
	// live/e2e/estates/ec2-core/README.md, "Untaggable types", for
	// the five untaggable types this batch also admits.
	"aws_instance",
	"aws_key_pair",
	"aws_placement_group",
	"aws_ec2_fleet",
	"aws_ec2_capacity_reservation",
	"aws_ec2_host",
	"aws_network_interface",
	"aws_spot_fleet_request",
}

var untaggableEc2Core = []string{
	// Registry-ratified EC2 core batch (#40, #44, issue #65): five
	// untaggable types — four whose Argument Reference names no tags
	// block at all, plus aws_ebs_snapshot_block_public_access, a
	// per-region singleton with no arguments at all beyond `state`. See
	// live/e2e/estates/ec2-core/README.md, "Untaggable types".
	"aws_network_interface_attachment",
	"aws_network_interface_permission",
	"aws_eip_association",
	"aws_volume_attachment",
	"aws_ebs_snapshot_block_public_access",
}

func init() {
	registerCohortStamp(taggableEc2Core, untaggableEc2Core, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified EC2 core batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type: aws_network_interface_attachment,
			// aws_network_interface_permission, aws_eip_association and
			// aws_volume_attachment carry no tags argument at all, and
			// aws_ebs_snapshot_block_public_access carries no argument beyond
			// `state`.
			"aws_instance":                         taggedSchema("id", "arn", "ami", "instance_type"),
			"aws_key_pair":                         taggedSchema("id", "arn", "key_name", "public_key"),
			"aws_placement_group":                  taggedSchema("id", "arn", "name", "strategy"),
			"aws_ec2_fleet":                        taggedSchema("id", "arn"),
			"aws_ec2_capacity_reservation":         taggedSchema("id", "arn", "instance_type", "availability_zone"),
			"aws_ec2_host":                         taggedSchema("id", "arn", "availability_zone"),
			"aws_network_interface":                taggedSchema("id", "arn", "subnet_id"),
			"aws_network_interface_attachment":     untaggedSchema("instance_id", "network_interface_id", "attachment_id", "device_index"),
			"aws_network_interface_permission":     untaggedSchema("network_interface_id", "aws_account_id", "permission", "network_interface_permission_id"),
			"aws_eip_association":                  untaggedSchema("id", "allocation_id", "instance_id"),
			"aws_volume_attachment":                untaggedSchema("device_name", "instance_id", "volume_id"),
			"aws_spot_fleet_request":               taggedSchema("id", "arn"),
			"aws_ebs_snapshot_block_public_access": untaggedSchema("state"),
		})
	})
}
