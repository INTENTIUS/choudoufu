// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableEc2Core is the ec2-core cohort's slice of [DefaultTable]:
// the identity rows the ec2-core ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableEc2Core = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, EC2 core (instances,
	// ---- EBS, ENI; issue #65) -------------------------------------------
	//
	// Same pipeline as the three batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour at the pinned v6.58.0
	// tag (its "Import" section and, where the provider has one, its own
	// identity schema) rather than accepted on the registry's word alone.
	// Scope is "instances and their periphery" — the slice issue #65 itself
	// names "EC2 core (instances, EBS, ENI)" — not the full 114-type EC2
	// registry service tools/row-gen enumerates; the VPC/Transit
	// Gateway/VPN/Client VPN/IPAM/Verified Access/route-server/NAT-gateway
	// families that make up the rest of that count are a future batch's
	// scope, not this one's. Cohort estate: live/e2e/estates/ec2-core.
	//
	// aws_instance is this batch's headline type: the repo's long-standing
	// canonical unadmitted example. live/e2e/limits/unadmitted-type and
	// live/LIMITATIONS.md's matching entry swap to aws_nat_gateway — a real,
	// non-logical, server-assigned EC2 type still in live/SURVEY.md's
	// curated 68, deliberately left out of this batch's own scope below and
	// out of every batch issue #65 names next, so it stays a stable example
	// rather than one this same wave of ratification would immediately have
	// to re-swap. See that fixture's own comment for the rest of the
	// account.
	//
	// Rejected, and deliberately absent from this table: none. Every
	// pastable proposal row-gen made in this batch's instances/EBS/ENI
	// slice checked out against the provider's real import behaviour — a
	// first for a registry-ratified batch, and worth naming precisely
	// because the other three batches all found at least one CFN-says-one-
	// thing-provider-says-another mismatch.
	//
	// Out of scope for this batch, not rejected on the evidence:
	//
	//   - aws_ec2_instance_connect_endpoint: a real, server-assigned,
	//     cleanly-proposed type (row-gen: primary identifier Id, read-only
	//     and not create-only), but it is SSH/RDP connectivity
	//     infrastructure for reaching an instance, not part of the
	//     instance's own identity, EBS, or ENI periphery this batch's
	//     mandate covers. Left for a networking-focused batch.
	//   - aws_nat_gateway_eip_association and
	//     aws_network_interface_sg_attachment: both evidence-only,
	//     property-children row-gen folds onto a parent (AWS::EC2::NatGateway
	//     and AWS::EC2::NetworkInterface respectively) with no pastable row
	//     of their own (issue #44's own non-goals). The second parent,
	//     aws_network_interface, is ratified below; the first,
	//     aws_nat_gateway, is not admitted at all yet (SURVEY.md's
	//     blocked-emulator: floci loses subnet_id on read) and is out of
	//     this batch's instances/EBS/ENI scope regardless.
	//
	// aws_placement_group is the one correction this batch makes to
	// row-gen's own classification, not to the provider's identity:
	// row-gen filed it evidence-only because registry.json's primary
	// identifier (GroupName) does not string-match the provider's own
	// argument name (name) closely enough for row-gen's classifier to
	// paste a row (issue #44's own non-goal — no fuzzy matching between a
	// CFN field name and a provider argument name). The provider's real,
	// documented import command settles it independently of the registry:
	// client-named via `name`, the same shape as aws_key_pair alongside it
	// below.

	serverAssigned("aws_instance",
		"EC2 mints the instance ID (i-…) at create time; ami, instance_type, subnet_id and the rest of the launch configuration describe what to launch, not what comes back. Confirmed against the provider's own identity schema (v6.58.0: required id) and its documented import command.",
		"INSTANCEID", "id"),
	serverAssigned("aws_ec2_fleet",
		"EC2 mints the fleet's own identifier (fleet-…) at create time; the type's launch_template_config and target_capacity_specification blocks describe what to launch, not the fleet's own identity.",
		"FLEETID", "id"),
	serverAssigned("aws_ec2_capacity_reservation",
		"EC2 mints the reservation's ID (cr-…) at create time; instance_type, instance_platform and availability_zone in configuration describe what capacity to reserve, not the reservation's own identity.",
		"ID", "id"),
	serverAssigned("aws_ec2_host",
		"EC2 mints the dedicated host's ID (h-…) at create time; availability_zone and instance_type/instance_family in configuration describe what the host supports, not the host's own identity.",
		"HOSTID", "id"),
	serverAssigned("aws_network_interface",
		"EC2 mints the ENI's ID (eni-…) at create time; subnet_id in configuration names where the interface lives, not the interface itself. Confirmed against the provider's own identity schema (v6.58.0: required id).",
		"ID", "id"),
	serverAssigned("aws_network_interface_attachment",
		"EC2 mints the attachment's own ID (eni-attach-…) at create time, distinct from the instance_id and network_interface_id arguments that name the two ends of the attachment; the provider's own docs export no id attribute for this type at all, only attachment_id, which is also the documented import ID.",
		"ATTACHMENTID", "attachment_id"),
	serverAssigned("aws_network_interface_permission",
		"EC2 mints the permission's own ID at create time, distinct from the network_interface_id, aws_account_id and permission arguments that describe what is granted; the provider's own docs export no id attribute for this type, only network_interface_permission_id, which is also the documented import ID.",
		"NETWORKINTERFACEPERMISSIONID", "network_interface_permission_id"),
	serverAssigned("aws_eip_association",
		"EC2 mints the association's own ID (eipassoc-…) at create time, distinct from the allocation_id, instance_id and network_interface_id arguments that name what is being associated.",
		"ID", "id"),
	serverAssigned("aws_spot_fleet_request",
		"EC2 mints the spot fleet request's ID (sfr-…) at create time; the type's launch_specification/launch_template_config and target_capacity arguments describe what to launch, not the request's own identity.",
		"ID", "id"),

	TypeIdentity{
		// registry.json: primary_identifier=["KeyName"], in
		// create_only_properties and not in read_only_properties —
		// client-named. Confirmed directly against the provider's own
		// documented import command (terraform import aws_key_pair.deployer
		// deployer-key) and its Attribute Reference, which states id "The
		// key pair name."
		Type:          "aws_key_pair",
		Components:    []Component{attr("key_name")},
		ImportSyntax:  "KEY_NAME",
		IdentityAttrs: []string{"id", "key_name"},
	},
	TypeIdentity{
		// row-gen classified this evidence-only (see the batch comment
		// above for why); the provider's real, documented import command
		// settles it anyway: "terraform import aws_placement_group.prod_pg
		// production-placement-group" imports by the group's own `name`
		// argument, and the Attribute Reference confirms id "The name of
		// the placement group." — the same client-named shape as
		// aws_key_pair just above.
		Type:          "aws_placement_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classified this needs-hand-separator: registry.json's
		// primary identifier is the pair [VolumeId, InstanceId], a
		// composite with no separator any schema names (issue #44's own
		// non-goal). The separator is not a guess here: live/import-
		// grammar.json's scrape of the provider's own Import section names
		// it directly — DEVICE_NAME:VOLUME_ID:INSTANCE_ID — and the
		// provider's own identity schema (v6.58.0) requires exactly those
		// three arguments, all Required in the Argument Reference too, so
		// any realistic configuration already has them. Parent-derived over
		// aws_ebs_volume (already admitted) and aws_instance (ratified
		// above in this same batch): resolving this type needs both to
		// resolve first. The provider's docs export no additional
		// id-shaped attribute for this type at all — only the three
		// arguments read back — so no alias beyond them is claimed here,
		// the same standard of care aws_route's synthesized id gets.
		Type: "aws_volume_attachment",
		Components: []Component{
			attr("device_name"),
			sep(":"),
			attr("volume_id"),
			sep(":"),
			attr("instance_id"),
		},
		ImportSyntax:  "DEVICE_NAME:VOLUME_ID:INSTANCE_ID",
		IdentityAttrs: []string{"device_name", "instance_id", "volume_id"},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via registry.json's
		// AccountId (AWS::EC2::SnapshotBlockPublicAccess's primary
		// identifier) — the same singleton-per-account shape as the
		// IAM/ECR batch's three ECR registry-level types. The provider
		// disagrees about the shape, not the singleton-ness: its own
		// identity schema requires nothing at all for import (account_id
		// and region are both Optional), and its documented import command
		// is always the fixed literal string "default" ("terraform import
		// aws_ebs_snapshot_block_public_access.example default"), not an
		// account ID the account happens to have. This is a per-region
		// settings object AWS gives every region exactly one of, not a
		// value AWS mints per resource, so it needs no discovery at all:
		// Components below is a pure literal, computable from configuration
		// with nothing to look up — ServerAssigned is deliberately false,
		// unlike every other row in this batch. The provider's own docs say
		// this resource "exports no additional attributes", so no
		// IdentityAttrs are claimed either, the same standard of care
		// aws_route's synthesized id gets.
		Type:          "aws_ebs_snapshot_block_public_access",
		Components:    []Component{sep("default")},
		ImportSyntax:  "default",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableEc2Core) }
