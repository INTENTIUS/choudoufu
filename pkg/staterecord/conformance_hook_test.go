// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord_test

import (
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/pkg/staterecord/staterecordtest"
)

// The in-package store tests run the exported suite (export_test.go).
func init() {
	staterecord.SetConformanceSuiteForTest(staterecordtest.RunConformance)
}
