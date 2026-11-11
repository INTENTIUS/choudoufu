// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func decodeStrictSSMSrc(t *testing.T, src string) (*LiveStrictSSM, hcl.Diagnostics) {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	content, diags := f.Body.Content(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "ssm"}}})
	if diags.HasErrors() || len(content.Blocks) != 1 {
		t.Fatalf("parsing: %s", diags.Error())
	}
	return decodeStrictSSMBlock(content.Blocks[0])
}

// TestStrictSSMAllowInsecure: the ssm block's waiver of the first-contact
// SSM contract (GitHub issue #2393, unit S2) takes the contract's two
// setting names and refuses anything else, by name, the way a record
// store's allow_insecure does.
func TestStrictSSMAllowInsecure(t *testing.T) {
	sm, diags := decodeStrictSSMSrc(t, `ssm {
  kms_key_id     = "alias/team-secrets"
  allow_insecure = ["customer_managed_key", "parameter_access"]
}`)
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	if !sm.AllowInsecureSet || !slices.Equal(sm.AllowInsecure, StrictSSMInsecureSettings) {
		t.Errorf("AllowInsecure = %v (set %v), want %v", sm.AllowInsecure, sm.AllowInsecureSet, StrictSSMInsecureSettings)
	}

	sm, diags = decodeStrictSSMSrc(t, `ssm { kms_key_id = "alias/team-secrets" }`)
	if diags.HasErrors() || sm.AllowInsecureSet || sm.AllowInsecure != nil {
		t.Errorf("an omitted allow_insecure decoded as %v (set %v), %s", sm.AllowInsecure, sm.AllowInsecureSet, diags.Error())
	}

	for _, tc := range []struct{ name, src, want string }{
		{"a bucket setting", `ssm {
  kms_key_id     = "k"
  allow_insecure = ["versioning"]
}`, `names "versioning"`},
		{"a typo", `ssm {
  kms_key_id     = "k"
  allow_insecure = ["customer_managed_keys"]
}`, "customer_managed_key"},
		{"a duplicate", `ssm {
  kms_key_id     = "k"
  allow_insecure = ["parameter_access", "parameter_access"]
}`, "more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm, diags := decodeStrictSSMSrc(t, tc.src)
			if !diags.HasErrors() || !strings.Contains(diags.Error(), "Invalid ssm allow_insecure") || !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("diagnostics %q, want an Invalid ssm allow_insecure containing %q", diags.Error(), tc.want)
			}
			if sm.AllowInsecureSet {
				t.Errorf("a refused waiver was kept: %v", sm.AllowInsecure)
			}
		})
	}
}
