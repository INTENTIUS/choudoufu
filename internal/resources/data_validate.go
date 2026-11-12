// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// choudoufu fork port (#2414): remove when upstream #4058 and #4740 land.
// Cherry-picked from upstream #4740 (commit 4b689c442b) with the import path
// rewritten; upstream's own copy replaces this file.

package resources

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ValidateConfig asks the provider whether the given value is valid.
//
// The given value should already conform to the schema of the resource type.
func (dt *DataResourceType) ValidateConfig(ctx context.Context, configVal cty.Value) tfdiags.Diagnostics {
	configValUnmarked, _ := configVal.UnmarkDeep()
	resp := dt.client.ValidateDataResourceConfig(ctx, providers.ValidateDataResourceConfigRequest{
		TypeName: dt.typeName,
		Config:   configValUnmarked,
	})
	return resp.Diagnostics
}
