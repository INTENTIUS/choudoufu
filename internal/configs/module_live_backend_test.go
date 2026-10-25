// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
)

// A backend block beside a live block is dropped with a warning that carries
// no source excerpt (GitHub issue #2226): Atmos generates backend.tf.json, and
// an excerpt of it holds a "{" that Atmos takes for the start of the JSON
// "show -json" prints after an init.
func TestLiveIgnoredBackendWarningHasNoExcerpt(t *testing.T) {
	p := testParser(map[string]string{
		"mod/main.tf": `
terraform {
  live {
    estate = "unit"
  }
}
`,
		"mod/backend.tf.json": `{
  "terraform": {
    "backend": {
      "s3": {
        "bucket": "state",
        "key": "terraform.tfstate"
      }
    }
  }
}
`,
	})
	mod, diags := p.LoadConfigDir("mod")
	if mod == nil || mod.Backend != nil || mod.IgnoredBackend == nil {
		t.Fatalf("the backend block was not dropped: %+v %v", mod, diags)
	}
	var found *hcl.Diagnostic
	for _, d := range diags {
		if d.Summary == "Backend configuration ignored under live resource markers" {
			found = d
		}
	}
	if found == nil {
		t.Fatalf("no warning: %v", diags)
	}
	if found.Subject != nil || found.Context != nil {
		t.Errorf("the warning carries a source range, so it prints an excerpt: %v", found.Subject)
	}
	if !strings.Contains(found.Detail, "backend.tf.json:4") {
		t.Errorf("the detail no longer names the block's place: %s", found.Detail)
	}
}
