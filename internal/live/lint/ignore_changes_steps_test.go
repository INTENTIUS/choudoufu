// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1740, the #1645 follow-up. The Kubernetes ignore_changes
// guard compared an entry's path against the tofu-estate label path only,
// and compared steps by kind as well as name, so two things passed lint:
//
//   - the address annotation (markers.AddressAnnotation, #1639), which
//     every Kubernetes object carries since substrate.Kubernetes'
//     CarriesAddress flipped (#1641) and which a moved-block rename and the
//     #1640 sweep both depend on;
//   - any marker path spelled with index syntax where the carrier path uses
//     an attribute step, metadata[0]["labels"] or manifest["metadata"],
//     which HCL and the core's own ignore_changes treat as the same path.
//
// Each row is the issue's probe table (the annotation row is spelled at the
// path the annotation actually lives at, metadata[0].annotations[...]),
// plus the manifest-surface counterparts and the controls that must keep
// their current answer in both directions.
func TestIgnoreChangesMarkerPathSteps(t *testing.T) {
	const labelCfg = `
resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = [%s]
  }
}
`
	const manifestCfg = `
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
  }

  lifecycle {
    ignore_changes = [%s]
  }
}
`
	label := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}
	manifest := map[string]providers.Schema{"kubernetes_manifest": manifestSurfaceTestSchema()}

	cases := []struct {
		entry   string
		config  string
		schemas map[string]providers.Schema
		want    int
	}{
		// The issue's probe table.
		{`metadata[0].labels`, labelCfg, label, 1},
		{`metadata[0].annotations`, labelCfg, label, 1},
		{`metadata[0].annotations["choudoufu.intentius.io/tofu-address"]`, labelCfg, label, 1},
		{`manifest.metadata.annotations`, manifestCfg, manifest, 1},
		{`metadata[0]["labels"]`, labelCfg, label, 1},
		{`manifest["metadata"]`, manifestCfg, manifest, 1},
		{`manifest.metadata["labels"]`, manifestCfg, manifest, 1},

		// The same rules, one step further in each direction.
		{`metadata[0]["annotations"]`, labelCfg, label, 1},
		{`metadata[0]["labels"]["tofu-estate"]`, labelCfg, label, 1},
		{`metadata[0].labels.tofu-estate`, labelCfg, label, 1},
		{`manifest.metadata.annotations["choudoufu.intentius.io/tofu-address"]`, manifestCfg, manifest, 1},
		{`manifest["metadata"]["annotations"]`, manifestCfg, manifest, 1},
		{`manifest`, manifestCfg, manifest, 1},
		{`metadata`, labelCfg, label, 1},
		{`metadata[0]`, labelCfg, label, 1},

		// Controls: keys and arguments this mode does not write.
		{`metadata[0].labels["team"]`, labelCfg, label, 0},
		{`metadata[0]["labels"]["team"]`, labelCfg, label, 0},
		{`metadata[0].annotations["team"]`, labelCfg, label, 0},
		{`metadata[0]["annotations"]["team"]`, labelCfg, label, 0},
		{`metadata[0].name`, labelCfg, label, 0},
		{`data`, labelCfg, label, 0},
		{`manifest.metadata.annotations["team"]`, manifestCfg, manifest, 0},
		{`manifest["spec"]`, manifestCfg, manifest, 0},
		// A numeric index is not the attribute named by its digits: the
		// block's list index 1 is not index 0, and neither is a string "0".
		{`metadata[1].labels`, labelCfg, label, 0},
	}
	for _, tc := range cases {
		t.Run(tc.entry, func(t *testing.T) {
			dir := writeMainTF(t, fmt.Sprintf(tc.config, tc.entry))
			cfg := loadConfigDir(t, dir)
			got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: tc.schemas}))
			if len(got) != tc.want {
				t.Errorf("ignore_changes = [%s]: got %d RuleIgnoreChanges issues, want %d: %v", tc.entry, len(got), tc.want, got)
			}
		})
	}
}

// TestIgnoreChangesAnnotationDetailNamesTheAnnotation pins that a refusal
// over the address annotation says so, rather than explaining itself with
// the estate label the entry does not touch.
func TestIgnoreChangesAnnotationDetailNamesTheAnnotation(t *testing.T) {
	dir := writeMainTF(t, `resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = [metadata[0].annotations]
  }
}
`)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}
	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
	if want := "choudoufu.intentius.io/tofu-address"; !strings.Contains(got[0].Detail, want) {
		t.Errorf("detail does not name %q: %s", want, got[0].Detail)
	}
}
