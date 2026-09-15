// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

func manifestSeedSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest":        {Type: cty.DynamicPseudoType, Required: true},
			"object":          {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
			"computed_fields": {Type: cty.List(cty.String), Optional: true},
		},
	}}
}

// TestConfiguredAttrsSeedSeedsADynamicManifest: kubernetes_manifest's
// whole object is one dynamic argument written out in configuration, and
// the seed has to carry it so the projected prior holds the manifest the
// import never returns (GitHub issue #1079).
func TestConfiguredAttrsSeedSeedsADynamicManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata   = { name = "my-crontab", namespace = "default" }
    spec       = { cronSpec = "* * * * */5" }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	rc := cfg.Module.ManagedResources["kubernetes_manifest.crontab"]
	if rc == nil {
		t.Fatalf("fixture does not declare kubernetes_manifest.crontab; it declares %v", keysOfResources(cfg))
	}
	seed, _ := configuredAttrsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, manifestSeedSchema(), nil)
	got, ok := seed["manifest"]
	if !ok {
		t.Fatalf("manifest missing from the seed; got keys %v", seedKeys(seed))
	}
	if !got.Type().IsObjectType() || got.GetAttr("kind").AsString() != "CronTab" {
		t.Errorf("manifest seed = %#v, want the configuration's object", got)
	}
}
