// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"github.com/intentius/choudoufu/internal/engine/configadjust"
)

// The new runtime receives the Context's ConfigValueAdjuster as a
// configadjust.ConfigValueAdjuster (#2256 U4), and finds its optional
// capabilities by type assertion against configadjust's interfaces. These
// assignments fail to compile if either side's method set drifts from the
// other, which would otherwise make the new runtime silently skip a
// capability the old runtime calls.
var (
	_ configadjust.ConfigValueAdjuster       = ConfigValueAdjuster(nil)
	_ configadjust.CreateConfigValueAdjuster = CreateConfigValueAdjuster(nil)
	_ configadjust.PriorConfigValueAdjuster  = PriorConfigValueAdjuster(nil)
	_ configadjust.IgnoreChangesAdjuster     = IgnoreChangesAdjuster(nil)
	_ configadjust.AppliedMarkerVerifier     = AppliedMarkerVerifier(nil)
	_ configadjust.AppliedMarkerWriter       = AppliedMarkerWriter(nil)

	_ CreateConfigValueAdjuster = configadjust.CreateConfigValueAdjuster(nil)
	_ PriorConfigValueAdjuster  = configadjust.PriorConfigValueAdjuster(nil)
	_ IgnoreChangesAdjuster     = configadjust.IgnoreChangesAdjuster(nil)
	_ AppliedMarkerVerifier     = configadjust.AppliedMarkerVerifier(nil)
	_ AppliedMarkerWriter       = configadjust.AppliedMarkerWriter(nil)
)
