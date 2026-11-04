// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// Destroy-time arguments the AWS provider's delete reads from state, so
// that changing one in configuration and running destroy is ignored: the
// same shape as aws-rds-cluster-destroy-from-config. Each delete was read at
// v6.71.0 (#2309): every one is d.Get(...) inside the type's Delete func.
//
// The provider tracker has closed its reports of this class as working as
// designed or not reproduced (#428 for aws_s3_bucket, #33523 for
// aws_ecr_repository, #92 for aws_db_instance), so the open reference is
// OpenTofu's: opentofu/opentofu#3472, where the maintainers name
// force_destroy as the same problem and the fix as a protocol-level
// destroy-time argument. All of these retire together when that lands.
//
// aws_iam_role's force_detach_policies is not here: on real AWS at 6.71.0
// a stock destroy with it false and a policy attached outside OpenTofu
// detached the policy and deleted the role anyway (the provider's own
// hashicorp/terraform-provider-aws#47855), so there is nothing to fix.

const destroyTimeStock = "Applying the argument before destroying works in stock OpenTofu, as two runs; the provider documents that for force_destroy. OpenTofu treats destroy as state-only by design (opentofu/opentofu#3472, blocked)."

var _ = register(Override{
	Name:              "aws-db-instance-destroy-from-config",
	Provider:          awsProvider,
	Type:              "aws_db_instance",
	Approach:          RewriteConfig,
	SDK:               SDKv2,
	Upstream:          "opentofu/opentofu#3472",
	StockAlternative:  destroyTimeStock,
	Summary:           "Destroy ignores skip_final_snapshot, final_snapshot_identifier and delete_automated_backups as configured, using the values last applied",
	ProvenOn:          "6.71.0",
	Requires:          map[string]cty.Type{"skip_final_snapshot": cty.Bool, "final_snapshot_identifier": cty.String, "delete_automated_backups": cty.Bool},
	DestroyFromConfig: []string{"skip_final_snapshot", "final_snapshot_identifier", "delete_automated_backups"},
})

var _ = register(Override{
	Name:              "aws-s3-bucket-destroy-from-config",
	Provider:          awsProvider,
	Type:              "aws_s3_bucket",
	Approach:          RewriteConfig,
	SDK:               SDKv2,
	Upstream:          "opentofu/opentofu#3472",
	StockAlternative:  destroyTimeStock,
	Summary:           "Destroy ignores force_destroy as configured, so a bucket with objects fails to delete until force_destroy has been applied",
	ProvenOn:          "6.71.0",
	Requires:          map[string]cty.Type{"force_destroy": cty.Bool},
	DestroyFromConfig: []string{"force_destroy"},
})

var _ = register(Override{
	Name:              "aws-iam-user-destroy-from-config",
	Provider:          awsProvider,
	Type:              "aws_iam_user",
	Approach:          RewriteConfig,
	SDK:               SDKv2,
	Upstream:          "opentofu/opentofu#3472",
	StockAlternative:  destroyTimeStock,
	Summary:           "Destroy ignores force_destroy as configured, so a user with keys or a login profile made outside OpenTofu fails to delete until it has been applied",
	ProvenOn:          "6.71.0",
	Requires:          map[string]cty.Type{"force_destroy": cty.Bool},
	DestroyFromConfig: []string{"force_destroy"},
})

var _ = register(Override{
	Name:              "aws-route53-zone-destroy-from-config",
	Provider:          awsProvider,
	Type:              "aws_route53_zone",
	Approach:          RewriteConfig,
	SDK:               SDKv2,
	Upstream:          "opentofu/opentofu#3472",
	StockAlternative:  destroyTimeStock,
	Summary:           "Destroy ignores force_destroy as configured, so a zone with records made outside OpenTofu fails to delete until it has been applied",
	ProvenOn:          "6.71.0",
	Requires:          map[string]cty.Type{"force_destroy": cty.Bool},
	DestroyFromConfig: []string{"force_destroy"},
})

var _ = register(Override{
	Name:              "aws-ecr-repository-destroy-from-config",
	Provider:          awsProvider,
	Type:              "aws_ecr_repository",
	Approach:          RewriteConfig,
	SDK:               SDKv2,
	Upstream:          "opentofu/opentofu#3472",
	StockAlternative:  destroyTimeStock,
	Summary:           "Destroy ignores force_delete as configured, so a repository with images fails to delete until it has been applied",
	ProvenOn:          "6.71.0",
	Requires:          map[string]cty.Type{"force_delete": cty.Bool},
	DestroyFromConfig: []string{"force_delete"},
})
