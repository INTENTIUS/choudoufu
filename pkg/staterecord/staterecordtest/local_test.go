// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecordtest_test

import (
	"testing"

	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/pkg/staterecord/staterecordtest"
)

// TestPublicLocalStoreConformance is GitHub issue #2281's acceptance: a
// test in another package runs the exported suite against a store built
// only from the public API, the way another module (plunge) holds its own
// configuration to it.
func TestPublicLocalStoreConformance(t *testing.T) {
	staterecordtest.RunConformance(t, func(t *testing.T) staterecord.Store {
		t.Helper()
		s, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %v", err)
		}
		return s
	})
}
