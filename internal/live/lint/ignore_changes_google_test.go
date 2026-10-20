// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1119: the GCP labels map carries tofu-estate and the encoded
// tofu-address, so ignoring it, or either key in it, is refused exactly as
// the AWS tags map is (checkIgnoreChangesGoogleLabels).

func googleLabelsTestSchema() providers.Schema {
	m := cty.Map(cty.String)
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Required: true},
		"location":         {Type: cty.String, Required: true},
		"labels":           {Type: m, Optional: true},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}}
}

func TestIgnoreChangesGoogleLabels(t *testing.T) {
	schemas := map[string]providers.Schema{"google_storage_bucket": googleLabelsTestSchema()}
	for ignore, want := range map[string]int{
		`[labels]`:                 1,
		`[labels["tofu-estate"]]`:  1,
		`[labels["tofu-address"]]`: 1,
		`[labels["owner"]]`:        0,
		`all`:                      1,
	} {
		t.Run(ignore, func(t *testing.T) {
			dir := writeMainTF(t, fmt.Sprintf(`resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "EU"

  lifecycle {
    ignore_changes = %s
  }
}
`, ignore))
			got := ignoreChangesIssues(CheckWith(t.Context(), loadConfigDir(t, dir), Context{Schemas: schemas}))
			if len(got) != want {
				t.Fatalf("ignore_changes = %s: got %d RuleIgnoreChanges issues, want %d: %v", ignore, len(got), want, got)
			}
		})
	}
}
