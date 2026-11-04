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

const appConfigFlagsName = "aws-appconfig-hosted-configuration-version-flag-timestamps"

// appConfigConfigured is the repro's content as jsonencode writes it.
const appConfigConfigured = `{"flags":{"foo":{"name":"foo"}},"values":{"foo":{"enabled":true}},"version":"1"}`

// appConfigStored is that content as AppConfig hands it back for a
// feature-flags profile: the injected keys added under flags and values.
const appConfigStored = `{"flags":{"foo":{"name":"foo","_createdAt":"2026-11-04T10:00:00.000Z","_updatedAt":"2026-11-04T10:00:00.000Z"}},` +
	`"values":{"foo":{"enabled":true,"_createdAt":"2026-11-04T10:00:00.000Z","_updatedAt":"2026-11-04T10:00:00.000Z"}},"version":"1"}`

func appConfigFlagsValue(content string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                       cty.StringVal("abc1234/def5678/1"),
		"application_id":           cty.StringVal("abc1234"),
		"configuration_profile_id": cty.StringVal("def5678"),
		"content":                  cty.StringVal(content),
		"content_type":             cty.StringVal("application/json"),
		"version_number":           cty.NumberIntVal(1),
	})
}

func init() {
	config := appConfigFlagsValue(appConfigConfigured).AsValueMap()
	config["id"] = cty.NullVal(cty.String)
	config["version_number"] = cty.NullVal(cty.Number)
	proofs[appConfigFlagsName] = proof{
		schema: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                       {Type: cty.String, Computed: true},
			"application_id":           {Type: cty.String, Required: true},
			"configuration_profile_id": {Type: cty.String, Required: true},
			"content":                  {Type: cty.String, Required: true, Sensitive: true},
			"content_type":             {Type: cty.String, Required: true},
			"version_number":           {Type: cty.Number, Computed: true},
		}},
		forceNew: []string{"application_id", "configuration_profile_id", "content", "content_type"},
		prior:    appConfigFlagsValue(appConfigStored),
		config:   cty.ObjectVal(config),
	}
}

// TestAppConfigFlagChangeStillReplaces checks the override does not hide a
// real change: flipping the flag to enabled false, which is the repro's v2,
// still replaces the version though prior carries the injected keys.
func TestAppConfigFlagChangeStillReplaces(t *testing.T) {
	o, _ := Lookup(appConfigFlagsName)
	for name, content := range map[string]string{
		"flag flipped":      `{"flags":{"foo":{"name":"foo"}},"values":{"foo":{"enabled":false}},"version":"1"}`,
		"flag added":        `{"flags":{"foo":{"name":"foo"},"bar":{"name":"bar"}},"values":{"foo":{"enabled":true},"bar":{"enabled":true}},"version":"1"}`,
		"version changed":   `{"flags":{"foo":{"name":"foo"}},"values":{"foo":{"enabled":true}},"version":"2"}`,
		"timestamp set too": `{"flags":{"foo":{"name":"foo","_createdAt":"2026-11-04T10:00:00.000Z"}},"values":{"foo":{"enabled":true}},"version":"1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := proofs[o.Name]
			cfg := p.config.AsValueMap()
			cfg["content"] = cty.StringVal(content)
			p.config = cty.ObjectVal(cfg)
			resp := planWith(t, o, p, true)
			if len(resp.RequiresReplace) == 0 {
				t.Fatalf("a different content planned no replacement")
			}
			if hasWarning(resp.Diagnostics, "Provider override applied", o.Name) {
				t.Errorf("the override claims to have acted on a real change")
			}
		})
	}
}

// TestSameFlagContent pins the equivalence: only the injected keys, only
// under flags and values, only taken from prior.
func TestSameFlagContent(t *testing.T) {
	for _, tc := range []struct {
		name          string
		prior, config string
		want          bool
	}{
		{"injected keys only", appConfigStored, appConfigConfigured, true},
		{"injected keys and reformatted", "{\n  \"version\": \"1\",\n  \"values\": {\"foo\": {\"_updatedAt\": \"y\", \"enabled\": true}},\n  \"flags\": {\"foo\": {\"_createdAt\": \"x\", \"name\": \"foo\"}}\n}", appConfigConfigured, true},
		{"nothing injected, only reformatted", `{ "flags":{"foo":{"name":"foo"}},"values":{"foo":{"enabled":true}},"version":"1"}`, appConfigConfigured, false},
		{"injected key at the top level", `{"_createdAt":"x","flags":{"foo":{"name":"foo","_createdAt":"x"}},"values":{"foo":{"enabled":true}},"version":"1"}`, appConfigConfigured, false},
		{"injected key deeper down", `{"flags":{"foo":{"name":"foo","_createdAt":"x","attributes":{"a":{"_createdAt":"x"}}}},"values":{"foo":{"enabled":true}},"version":"1"}`,
			`{"flags":{"foo":{"name":"foo","attributes":{"a":{}}}},"values":{"foo":{"enabled":true}},"version":"1"}`, false},
		{"stripping config is not done", appConfigConfigured, appConfigStored, false},
		{"number spelled differently", `{"flags":{"foo":{"name":"foo","_createdAt":"x"}},"values":{"foo":{"enabled":true,"n":1.0}},"version":"1"}`,
			`{"flags":{"foo":{"name":"foo"}},"values":{"foo":{"enabled":true,"n":1}},"version":"1"}`, false},
		{"prior not JSON", "not json", appConfigConfigured, false},
		{"trailing content", appConfigStored + "{}", appConfigConfigured, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameFlagContent(cty.StringVal(tc.prior), cty.StringVal(tc.config)); got != tc.want {
				t.Errorf("sameFlagContent = %v, want %v", got, tc.want)
			}
		})
	}
}
