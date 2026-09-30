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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1539: an instance the static evaluator refused, which the
// node cannot resolve either, on a marker surface that carries no address,
// is refused at the node instead of planned as a create. The reproduction
// is the issue's own: a kubernetes_config_map_v1 whose name reads
// kubernetes_manifest.crontab.object.spec.image, a non-identity attribute.

func staticRefusal(addr addrs.AbsResourceInstance) map[string]tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Identity not resolvable from configuration",
		addr.String()+".name refers to kubernetes_manifest.crontab.object.spec.image, but an identity can only be built from a single attribute of another resource (its identity attribute)."))
	return map[string]tfdiags.Diagnostics{addr.String(): diags}
}

func readerAddr() addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map_v1", Name: "reader"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// readerConfig is the config map as the node sees it: name known (every
// apply after the first) or unknown (the first apply, before the
// CronTab exists).
func readerConfig(name cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"data":      cty.NullVal(cty.Map(cty.String)),
		"id":        cty.NullVal(cty.String),
		"immutable": cty.NullVal(cty.Bool),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": cty.NullVal(cty.Map(cty.String)),
			"labels":      cty.NullVal(cty.Map(cty.String)),
			"name":        name,
			"namespace":   cty.StringVal("m1116-res"),
		})}),
	})
}

func TestNodeResolver_StaticRefusalStandsOnAddresslessMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  cty.Value
		schema providers.Schema
	}{
		{"labels/name known", readerConfig(cty.StringVal("my-awesome-cron-image-reader")), configMapTypeSchema()},
		{"labels/name unknown", readerConfig(cty.UnknownVal(cty.String)), configMapTypeSchema()},
		{"manifest", cty.ObjectVal(map[string]cty.Value{"manifest": cty.UnknownVal(cty.DynamicPseudoType)}), manifestTypeSchema()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := readerAddr()
			resolver := &NodeResolver{StaticRefusals: staticRefusal(addr)}
			target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, tc.value, tc.schema)
			if found {
				t.Fatalf("found=true, target=%#v; want the refusal", target)
			}
			if !diags.HasErrors() || !hasDiagSummary(diags, SummaryIdentityUnresolvedNoAddress) {
				t.Fatalf("want an error %q, got: %v", SummaryIdentityUnresolvedNoAddress, diags.ErrWithWarnings())
			}
		})
	}
}

// Nothing refused statically: a greenfield instance plans its create as
// before, whatever its surface.
func TestNodeResolver_AddresslessMarkerWithoutStaticRefusalPlansCreate(t *testing.T) {
	addr := readerAddr()
	resolver := &NodeResolver{StaticRefusals: staticRefusal(locatedTestAddr(t, "kubernetes_config_map_v1", "other"))}
	_, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, readerConfig(cty.UnknownVal(cty.String)), configMapTypeSchema())
	if found || hasDiagSummary(diags, SummaryIdentityUnresolvedNoAddress) {
		t.Fatalf("found=%v diags=%v; a statically resolved instance must not be refused", found, diags.ErrWithWarnings())
	}
}

// The node resolves the instance (here through the marker index): the
// static refusal no longer matters.
func TestNodeResolver_AddresslessMarkerResolvedAtNodeIsBound(t *testing.T) {
	addr := readerAddr()
	resolver := &NodeResolver{
		StaticRefusals: staticRefusal(addr),
		MarkerIndex:    map[string]providers.ImportTarget{addr.String(): {ID: "m1116-res/my-awesome-cron-image-reader"}},
	}
	target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, readerConfig(cty.StringVal("x")), configMapTypeSchema())
	if diags.HasErrors() || !found || target.ID != "m1116-res/my-awesome-cron-image-reader" {
		t.Fatalf("target=%#v found=%v diags=%v", target, found, diags.ErrWithWarnings())
	}
}

// A marker that carries the address (the tag map) is untouched: the same
// static refusal leaves the node's existing answer exactly as it was.
func TestNodeResolver_TagMarkerIgnoresStaticRefusal(t *testing.T) {
	addr := locatedTestAddr(t, "aws_s3_bucket", "b")
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"bucket": {Type: cty.String, Optional: true},
		"tags":   {Type: cty.Map(cty.String), Optional: true},
	}}}
	value := cty.ObjectVal(map[string]cty.Value{"bucket": cty.UnknownVal(cty.String), "tags": cty.NullVal(cty.Map(cty.String))})

	without := &NodeResolver{}
	_, wantFound, wantDiags := without.ResolveResourceIdentity(context.Background(), addr, value, schema)

	with := &NodeResolver{StaticRefusals: staticRefusal(addr)}
	_, found, diags := with.ResolveResourceIdentity(context.Background(), addr, value, schema)
	if found != wantFound || len(diags) != len(wantDiags) || hasDiagSummary(diags, SummaryIdentityUnresolvedNoAddress) {
		t.Fatalf("tag surface changed: found %v->%v, diags %v -> %v", wantFound, found, wantDiags.ErrWithWarnings(), diags.ErrWithWarnings())
	}
}

// GitHub issue #1641: the Kubernetes surfaces carry the address in an
// annotation, so the refusal stands per object, on the sweep's account
// ([NodeResolver.UnaddressedObjects]).
func TestNodeResolver_AddressAnnotationRefusalStandsPerObject(t *testing.T) {
	addr := readerAddr()
	for _, tc := range []struct {
		name        string
		unaddressed map[string][]string
		wantRefuse  bool
		wantDetail  string
	}{
		{"the sweep listed every kind and found none", map[string][]string{addr.String(): {}}, false, ""},
		{"the sweep found an unannotated object", map[string][]string{addr.String(): {"ConfigMap m1116-res/old"}}, true, "found ConfigMap m1116-res/old carrying this estate's tofu-estate label and no annotation that binds it to this block"},
		{"the sweep found two", map[string][]string{addr.String(): {"ConfigMap ns/a", "ConfigMap ns/b"}}, true, "ConfigMap ns/a and ConfigMap ns/b"},
		{"the sweep did not account for the instance", nil, true, "could not list every kind"},
		{"the sweep accounted for another instance", map[string][]string{"kubernetes_config_map_v1.other": {}}, true, "could not list every kind"},
	} {
		for _, schema := range []struct {
			name   string
			value  cty.Value
			schema providers.Schema
		}{
			{"labels", readerConfig(cty.UnknownVal(cty.String)), configMapTypeSchema()},
			{"manifest", cty.ObjectVal(map[string]cty.Value{"manifest": cty.UnknownVal(cty.DynamicPseudoType)}), manifestTypeSchema()},
		} {
			t.Run(tc.name+"/"+schema.name, func(t *testing.T) {
				resolver := &NodeResolver{StaticRefusals: staticRefusal(addr), UnaddressedObjects: tc.unaddressed}
				_, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, schema.value, schema.schema)
				if found {
					t.Fatal("found an object; nothing is bound")
				}
				refused := hasDiagSummary(diags, SummaryIdentityUnresolvedNoAddress)
				if refused != tc.wantRefuse {
					t.Fatalf("refused = %v, want %v: %v", refused, tc.wantRefuse, diags.ErrWithWarnings())
				}
				if tc.wantRefuse && !strings.Contains(diags.ErrWithWarnings().Error(), tc.wantDetail) {
					t.Errorf("detail does not say %q: %v", tc.wantDetail, diags.ErrWithWarnings())
				}
			})
		}
	}
}
