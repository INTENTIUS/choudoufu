// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package offlineplan

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCheckStaysProviderFree is GitHub issue #2297's second acceptance
// item: the second pass lives in the callers, and internal/live/check still
// starts no provider. It imports neither this package nor the plugin
// launcher, directly or through anything else.
func TestCheckStaysProviderFree(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}",
		"github.com/intentius/choudoufu/internal/live/check").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := map[string]bool{
		"github.com/intentius/choudoufu/internal/live/offlineplan":  true,
		"github.com/intentius/choudoufu/internal/live/pluginschema": true,
		"github.com/hashicorp/go-plugin":                            true,
	}
	for _, dep := range strings.Fields(string(out)) {
		if forbidden[dep] {
			t.Errorf("internal/live/check imports %s; the second pass belongs in its callers (check.Context.ManagedResults)", dep)
		}
	}
}

// TestConfigValueKeepsAWSOffline: hashicorp/aws gets placeholder keys, the
// switches that stop it calling STS and the metadata service while it
// configures, and the dead proxy, each only where its schema declares the
// argument and converted to the declared type. Any other provider gets its
// empty configuration.
func TestConfigValueKeepsAWSOffline(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"region":                      {Type: cty.String, Optional: true},
		"access_key":                  {Type: cty.String, Optional: true},
		"secret_key":                  {Type: cty.String, Optional: true},
		"skip_credentials_validation": {Type: cty.Bool, Optional: true},
		"skip_requesting_account_id":  {Type: cty.Bool, Optional: true},
		"skip_metadata_api_check":     {Type: cty.String, Optional: true},
		"https_proxy":                 {Type: cty.String, Optional: true},
		"profile":                     {Type: cty.String, Optional: true},
	}}

	got := ConfigValue(addrs.NewDefaultProvider("aws"), block)
	for name, want := range map[string]cty.Value{
		"region":                      cty.StringVal("us-east-1"),
		"skip_credentials_validation": cty.True,
		"skip_requesting_account_id":  cty.True,
		"skip_metadata_api_check":     cty.StringVal("true"),
		"https_proxy":                 cty.StringVal(DeadProxy),
		"profile":                     cty.NullVal(cty.String),
	} {
		if v := got.GetAttr(name); !v.RawEquals(want) {
			t.Errorf("%s = %#v, want %#v", name, v, want)
		}
	}
	if got.Type().HasAttribute("http_proxy") {
		t.Errorf("an argument the schema does not declare was added: %#v", got)
	}

	other := ConfigValue(addrs.NewDefaultProvider("random"), block)
	if v := other.GetAttr("region"); !v.IsNull() {
		t.Errorf("a provider other than hashicorp/aws got region %#v; it must get its empty configuration", v)
	}
}

// TestManagedResultsWithoutProvidersDoesNothing: no providers means no
// second pass, and no values.
func TestManagedResultsWithoutProvidersDoesNothing(t *testing.T) {
	values, outcome := ManagedResults(t.Context(), nil, identity.Context{}, nil)
	if values != nil || outcome != OutcomeNotNeeded {
		t.Errorf("ManagedResults(nil config, nil providers) = %v, %q; want nil, %q", values, outcome, OutcomeNotNeeded)
	}
}
