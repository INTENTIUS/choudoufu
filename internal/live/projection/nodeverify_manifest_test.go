// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestVerifyAppliedMarkersManifestSurface is the manifest arm of
// carrierMarkers (GitHub issue #1585). The tag and label arms each had a
// test here; the manifest arm had none, so deleting it left this package
// green and only the completeness guard named it. A kubernetes_manifest
// whose estate label was stripped must fire, and one that kept it must not.
func TestVerifyAppliedMarkersManifestSurface(t *testing.T) {
	n := &NodeResolver{Estate: "prod"}
	addr := manifestAddr(t)
	schema := manifestTypeSchema()
	planned := manifestTestConfig(manifestTestManifest(cty.ObjectVal(map[string]cty.Value{
		"app":         cty.StringVal("crontab"),
		"tofu-estate": cty.StringVal("prod"),
	})))

	t.Run("stripped label fires", func(t *testing.T) {
		applied := manifestTestConfig(manifestTestManifest(cty.ObjectVal(map[string]cty.Value{
			"app": cty.StringVal("crontab"),
		})))
		sev, detail := severityOf(t, n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema))
		if sev != tfdiags.Error {
			t.Errorf("an update that lost tofu-estate must be an error, got severity %v", sev)
		}
		if !strings.Contains(detail, `tofu-estate: sent "prod", not stored`) {
			t.Errorf("detail does not name the lost label:\n%s", detail)
		}
	})

	t.Run("label stored is silent", func(t *testing.T) {
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, planned, schema); len(diags) != 0 {
			t.Fatalf("a label that landed must say nothing, got: %s", diags.Err())
		}
	})
}
