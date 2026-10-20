// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"slices"
	"testing"

	"github.com/intentius/choudoufu/internal/live/absent"
)

// TestARMProvidersAreTheAzureFamily holds internal/live/absent's copy of
// the Azure family's provider types equal to [Azure]'s own (GitHub issue
// #2140, finding 1). absent is a leaf internal/tofu imports, so it cannot
// ask this package; a provider added here and not there would have its
// 404s read as failures, and one added there and not here would have
// another family's errors read as Azure absences.
func TestARMProvidersAreTheAzureFamily(t *testing.T) {
	if got, want := absent.ARMProviders, Azure.ProviderTypes(); !slices.Equal(got, want) {
		t.Errorf("absent.ARMProviders = %v, Azure.ProviderTypes() = %v", got, want)
	}
}
