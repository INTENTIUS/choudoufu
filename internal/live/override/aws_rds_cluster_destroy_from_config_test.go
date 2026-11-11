// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/providers"
)

var rdsClusterSchema = &configschema.Block{Attributes: map[string]*configschema.Attribute{
	"id":                        {Type: cty.String, Computed: true},
	"cluster_identifier":        {Type: cty.String, Required: true},
	"skip_final_snapshot":       {Type: cty.Bool, Optional: true},
	"final_snapshot_identifier": {Type: cty.String, Optional: true},
}}

// The rds-snapshot-2588 repro: applied with skip_final_snapshot = false,
// then flipped to true in configuration before destroy.
var rdsPrior = cty.ObjectVal(map[string]cty.Value{
	"id":                        cty.StringVal("c"),
	"cluster_identifier":        cty.StringVal("c"),
	"skip_final_snapshot":       cty.False,
	"final_snapshot_identifier": cty.NullVal(cty.String),
})

func rdsConfig(skip cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                        cty.NullVal(cty.String),
		"cluster_identifier":        cty.StringVal("c"),
		"skip_final_snapshot":       skip,
		"final_snapshot_identifier": cty.NullVal(cty.String),
	})
}

func init() {
	proofs["aws-rds-cluster-destroy-from-config"] = proof{check: checkDestroyFromConfig}
}

// checkDestroyFromConfig is red-then-green: an unwrapped provider's destroy
// keeps the state's skip_final_snapshot = false, a wrapped one takes the
// configured true and says so.
func checkDestroyFromConfig(t *testing.T, o Override) {
	ctx := context.Background()
	fake := &fakeProvider{typeName: o.Type, proof: proof{schema: rdsClusterSchema}}

	off, _ := DestroyConfig(ctx, fake, o.Type, rdsPrior, rdsConfig(cty.True))
	if !off.GetAttr("skip_final_snapshot").RawEquals(cty.False) {
		t.Fatalf("without the override the destroy already used the configured value, so this proof cannot fail")
	}
	if got := DestroyAttributes(ctx, fake, o.Type); len(got) != 0 {
		t.Fatalf("an unwrapped provider reports destroy attributes %v", got)
	}

	wrapped := Wrap(fake, map[string][]Override{o.Type: {o}})
	if got := DestroyAttributes(ctx, wrapped, o.Type); len(got) != 2 {
		t.Fatalf("DestroyAttributes = %v, want skip_final_snapshot and final_snapshot_identifier", got)
	}
	on, diags := DestroyConfig(ctx, wrapped, o.Type, rdsPrior, rdsConfig(cty.True))
	if !on.GetAttr("skip_final_snapshot").RawEquals(cty.True) {
		t.Errorf("with the override the destroy still sends skip_final_snapshot = %#v", on.GetAttr("skip_final_snapshot"))
	}
	if !on.GetAttr("cluster_identifier").RawEquals(cty.StringVal("c")) {
		t.Errorf("an attribute the override does not name changed: %#v", on.GetAttr("cluster_identifier"))
	}
	if !hasWarning(diags, "Provider override applied", o.Name) {
		t.Errorf("the destroy does not name the override that changed it: %#v", diags)
	}
}

// TestDestroyFromConfigNeverGuesses checks a configured value that is
// unknown, null or sensitive leaves the state's value, and an unchanged one
// says nothing.
func TestDestroyFromConfigNeverGuesses(t *testing.T) {
	ctx := context.Background()
	o, _ := Lookup("aws-rds-cluster-destroy-from-config")
	var fake providers.Interface = &fakeProvider{typeName: o.Type, proof: proof{schema: rdsClusterSchema}}
	wrapped := Wrap(fake, map[string][]Override{o.Type: {o}})
	for name, cfg := range map[string]cty.Value{
		"unknown":   rdsConfig(cty.UnknownVal(cty.Bool)),
		"null":      rdsConfig(cty.NullVal(cty.Bool)),
		"sensitive": rdsConfig(cty.True.Mark(marks.Sensitive)),
		"unchanged": rdsConfig(cty.False),
	} {
		got, diags := DestroyConfig(ctx, wrapped, o.Type, rdsPrior, cfg)
		if !got.RawEquals(rdsPrior) {
			t.Errorf("%s: prior changed to %#v", name, got)
		}
		if len(diags) != 0 {
			t.Errorf("%s: diagnostics %#v", name, diags)
		}
	}
}

// unwrappingProvider stands in for internal/tofu's parallelProvider: a
// provider wrapper above the override wrapper that hands back what it wraps.
type unwrappingProvider struct{ providers.Configured }

func (u unwrappingProvider) UnwrapProvider() providers.Configured { return u.Configured }

// TestDestroyFromConfigFindsTheOverrideUnderAListerOrAnotherWrapper: every
// plugin provider can list, so Wrap returns *wrappedLister for it (#2324),
// and an engine may wrap the provider again. Destroy-from-config has to find
// the overrides under both, or it silently never acts on a real provider.
func TestDestroyFromConfigFindsTheOverrideUnderAListerOrAnotherWrapper(t *testing.T) {
	ctx := context.Background()
	var o Override
	for _, c := range All() {
		if c.Type == "aws_rds_cluster" && len(c.DestroyFromConfig) > 0 {
			o = c
		}
	}
	if o.Type == "" {
		t.Fatal("no aws_rds_cluster destroy-from-config override registered")
	}
	lister := &listingFake{fakeProvider: fakeProvider{typeName: o.Type, proof: proof{schema: rdsClusterSchema}}}
	underLister := Wrap(lister, map[string][]Override{o.Type: {o}})
	for name, p := range map[string]providers.Interface{
		"plugin provider that lists":     underLister,
		"wrapped again above the lister": unwrappingProvider{underLister},
	} {
		if got := DestroyAttributes(ctx, p, o.Type); len(got) == 0 {
			t.Errorf("%s: DestroyAttributes found no override", name)
		}
		on, _ := DestroyConfig(ctx, p, o.Type, rdsPrior, rdsConfig(cty.True))
		if !on.GetAttr("skip_final_snapshot").RawEquals(cty.True) {
			t.Errorf("%s: the destroy still sends skip_final_snapshot = %#v", name, on.GetAttr("skip_final_snapshot"))
		}
	}
}
