// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// amplifyHeadersYAML is live/override-repro/amplify-headers-34318's
// configuration, and amplifyHeadersJSON is what AWS returned for it.
const (
	amplifyHeadersYAML = `customHeaders:
  - pattern: '**/*'
    headers:
      - key: 'Strict-Transport-Security'
        value: 'max-age=31536000; includeSubDomains'
`
	amplifyHeadersJSON = `[{"pattern":"**/*","headers":[{"key":"Strict-Transport-Security","value":"max-age=31536000; includeSubDomains"}]}]`
)

func init() {
	proofs["aws-amplify-app-custom-headers"] = proof{
		schema: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Required: true},
			// Optional and Computed in the provider. Declared Optional here
			// so the fake plans it from config, as SDKv2 does for a
			// configured value, rather than from prior.
			"custom_headers": {Type: cty.String, Optional: true},
		}},
		prior: cty.ObjectVal(map[string]cty.Value{
			"id":             cty.StringVal("d1abcdef"),
			"name":           cty.StringVal("x-app"),
			"custom_headers": cty.StringVal(amplifyHeadersJSON),
		}),
		config: cty.ObjectVal(map[string]cty.Value{
			"id":             cty.NullVal(cty.String),
			"name":           cty.StringVal("x-app"),
			"custom_headers": cty.StringVal(amplifyHeadersYAML),
		}),
	}
}

// TestAmplifyCustomHeadersRealChangeStillPlans checks the override does not
// hide a real change: a different header value still plans an update to the
// configured text.
func TestAmplifyCustomHeadersRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-amplify-app-custom-headers")
	p := proofs[o.Name]
	changed := `customHeaders:
  - pattern: '**/*'
    headers:
      - key: 'Strict-Transport-Security'
        value: 'max-age=63072000; includeSubDomains'
`
	cfg := p.config.AsValueMap()
	cfg["custom_headers"] = cty.StringVal(changed)
	p.config = cty.ObjectVal(cfg)
	resp := planWith(t, o, p, true)
	if got := resp.PlannedState.GetAttr("custom_headers"); !got.RawEquals(cty.StringVal(changed)) {
		t.Fatalf("a different header value planned %#v, want the configured text", got)
	}
}

func TestSameCustomHeaders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		want   bool
	}{
		{"repro yaml", amplifyHeadersYAML, true},
		{"json with the root key", `{"customHeaders":` + amplifyHeadersJSON + `}`, true},
		{"json without the root key", amplifyHeadersJSON, true},
		{"keys reordered, unquoted, commented", `# a comment
customHeaders:
  - headers:
      - value: "max-age=31536000; includeSubDomains"
        key: Strict-Transport-Security
    pattern: "**/*"
`, true},
		{"different value", `customHeaders:
  - pattern: '**/*'
    headers:
      - key: Strict-Transport-Security
        value: max-age=1
`, false},
		{"different pattern", `customHeaders:
  - pattern: '**/*.html'
    headers:
      - key: Strict-Transport-Security
        value: 'max-age=31536000; includeSubDomains'
`, false},
		{"extra header", `customHeaders:
  - pattern: '**/*'
    headers:
      - key: Strict-Transport-Security
        value: 'max-age=31536000; includeSubDomains'
      - key: X-Frame-Options
        value: DENY
`, false},
		{"root key beside another key is not stripped", `customHeaders:
  - pattern: '**/*'
    headers:
      - key: Strict-Transport-Security
        value: 'max-age=31536000; includeSubDomains'
other: 1
`, false},
		{"two documents", amplifyHeadersYAML + "---\n" + amplifyHeadersYAML, false},
		{"merge key", `base: &b
  key: Strict-Transport-Security
customHeaders:
  - pattern: '**/*'
    headers:
      - <<: *b
        value: 'max-age=31536000; includeSubDomains'
`, false},
		{"not yaml", "customHeaders: [", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sameCustomHeaders(cty.StringVal(amplifyHeadersJSON), cty.StringVal(tc.config))
			if got != tc.want {
				t.Errorf("sameCustomHeaders = %v, want %v", got, tc.want)
			}
		})
	}
}
