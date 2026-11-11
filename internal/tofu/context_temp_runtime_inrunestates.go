// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
)

// choudoufu fork port (#2370): remove when upstream #4058 lands, with the
// package. The new runtime receives ContextOpts.InRunEstates as an
// inrunestates.Estates. These assignments fail to compile if the two types
// drift apart. TestInRunEstatesPortMatchesTheDefaultRuntime checks the
// copied type name and refusals.
var (
	_ inrunestates.Estates    = ContextOpts{}.InRunEstates
	_ map[string]addrs.Module = inrunestates.Estates(nil)
)
