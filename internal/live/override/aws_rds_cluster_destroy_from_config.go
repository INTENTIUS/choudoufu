// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// The provider's delete reads skip_final_snapshot and
// final_snapshot_identifier from state, so changing them in configuration
// and running destroy is ignored. Reproduced on real AWS with provider
// 6.71.0 on 2026-11-04 (#2309): with skip_final_snapshot flipped to true,
// destroy failed with "final_snapshot_identifier is required when
// skip_final_snapshot is false". The same bug from the core side is
// opentofu/opentofu#3472.
var _ = register(Override{
	Name:             "aws-rds-cluster-destroy-from-config",
	Provider:         awsProvider,
	Type:             "aws_rds_cluster",
	Approach:         RewriteConfig,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#2588",
	StockAlternative: "Applying the flag before destroying works in stock OpenTofu, as two runs. OpenTofu treats destroy as state-only by design and calls the real fix a protocol-level destroy-time argument (opentofu/opentofu#3472, blocked).",
	AlsoTracks:       []string{"opentofu/opentofu#3472"},
	Summary:          "Destroy ignores skip_final_snapshot and final_snapshot_identifier as configured, using the values last applied",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"skip_final_snapshot":       cty.Bool,
		"final_snapshot_identifier": cty.String,
	},
	DestroyFromConfig: []string{"skip_final_snapshot", "final_snapshot_identifier"},
})
