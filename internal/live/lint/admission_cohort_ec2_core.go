// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesEc2Core is the ec2-core cohort's slice of [admittedTypesV0]:
// the types the ec2-core ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesEc2Core = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, EC2 core (instances,
	// ---- EBS, ENI; issue #65's own next-batch suggestion). Same evidence
	// ---- source and verification standard as the three batches above; see
	// ---- internal/live/identity/table.go for the per-type evidence and for
	// ---- the row-gen proposals this batch rejected or left out of scope.
	// ---- Cohort estate: live/e2e/estates/ec2-core. aws_instance is this
	// ---- batch's headline type: the repo's long-standing canonical
	// ---- unadmitted example (live/e2e/limits/unadmitted-type,
	// ---- live/LIMITATIONS.md) swaps to aws_nat_gateway in the same change —
	// ---- see that fixture's own comment for why.
	"aws_instance":                         {},
	"aws_key_pair":                         {},
	"aws_placement_group":                  {},
	"aws_ec2_fleet":                        {},
	"aws_ec2_capacity_reservation":         {},
	"aws_ec2_host":                         {},
	"aws_network_interface":                {},
	"aws_network_interface_attachment":     {},
	"aws_network_interface_permission":     {},
	"aws_eip_association":                  {},
	"aws_volume_attachment":                {},
	"aws_spot_fleet_request":               {},
	"aws_ebs_snapshot_block_public_access": {},
}

func init() { registerCohortAdmitted(admittedTypesEc2Core) }
