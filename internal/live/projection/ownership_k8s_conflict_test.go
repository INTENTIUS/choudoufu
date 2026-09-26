// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1546, ruled 2026-09-26: declared_untagged's default refuses
// a declared Kubernetes object when its identity is a key the API server
// enforces as unique AND this run positively read the object on the
// cluster. Before the ruling the same object read ADOPTABLE, the run exited
// 0 with one warning, and the plan proposed a create the API server answers
// with 409 AlreadyExists - measured on corpus-eks-basic's aws-auth ConfigMap
// (kube-system/aws-auth). A built-in type's block is never submitted to the
// server's dry run, so nothing else stood between that plan and the 409.
//
// The summary is spelled out here rather than read off the constant so a
// rename of the operator-facing text is a visible test change.
const conflictSummary = "Unlabelled live object holds the declared name"

func errorDiagsWith(diags tfdiags.Diagnostics, summary string) []tfdiags.Diagnostic {
	var out []tfdiags.Diagnostic
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == summary {
			out = append(out, d)
		}
	}
	return out
}

// TestK8sConflict_unlabelledReadObjectIsAnError is the ruling's first
// acceptance line on the label surface: the object was read, it carries no
// estate label, and the verb is the default refuse - whether left out or
// written explicitly. The run must fail with an Error naming the object and
// the setting that adopts it, instead of warning and planning the create.
func TestK8sConflict_unlabelledReadObjectIsAnError(t *testing.T) {
	for name, own := range map[string]*Ownership{
		"no policy block":          {Estate: k8sOwnershipEstate},
		"declared_untagged=refuse": {Estate: k8sOwnershipEstate, Policy: buildPolicy(t, "", "refuse")},
	} {
		t.Run(name, func(t *testing.T) {
			b := k8sOwnershipBuilder(own)
			addr := k8sConfigMapAddr(t)

			verdict := b.checkOwnership(addr, configMapTestType, "smoke-k8s/app-config",
				configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false)

			if verdict != ownershipUnowned {
				t.Fatalf("checkOwnership = %d, want ownershipUnowned (%d)", verdict, ownershipUnowned)
			}
			errs := errorDiagsWith(b.diags, conflictSummary)
			if len(errs) != 1 {
				t.Fatalf("want exactly one Error %q, got:\n%s", conflictSummary, renderDiags(b.diags))
			}
			detail := errs[0].Description().Detail
			for _, want := range []string{
				"smoke-k8s/app-config",
				addr.String(),
				configMapTestType,
				"carries no " + markers.TagEstate + " label",
				"409",
				`policy { declared_untagged = "adopt" }`,
				markers.TagEstate + `="` + k8sOwnershipEstate + `"`,
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, detail)
				}
			}
			if strings.Contains(detail, markers.TagAddress) {
				t.Errorf("the Kubernetes refusal names a %s marker, which #1016 ruled does not exist there:\n%s", markers.TagAddress, detail)
			}
			if hasDiag(b.diags, SummaryOutsideEstate, "smoke-k8s/app-config") {
				t.Errorf("the object is also reported under the warning, so it reads twice:\n%s", renderDiags(b.diags))
			}
			if len(b.unownedList) != 1 {
				t.Errorf("Unowned = %+v, want the one object", b.unownedList)
			}
			if om, ok := b.omitted[addr.String()]; !ok || om.Reason != ReasonUnowned {
				t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
			}
		})
	}
}

// TestK8sConflict_whatTheRuleDoesNotReach pins every boundary the ruling
// draws around the new Error. Each of these passes before the change and
// must keep passing after it.
func TestK8sConflict_whatTheRuleDoesNotReach(t *testing.T) {
	t.Run("adopt admits the object", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "adopt")})
		if got := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
			configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false); got != ownershipOK {
			t.Fatalf("declared_untagged = adopt did not admit: %d", got)
		}
		if b.diags.HasErrors() {
			t.Errorf("adopt raised an error:\n%s", renderDiags(b.diags))
		}
	})

	// keep and report are an operator's explicit choice of a softer
	// refusal. The ruling is about the default; these keep their meaning.
	for _, verb := range []string{"keep", "report"} {
		t.Run(verb+" is unchanged", func(t *testing.T) {
			b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", verb)})
			if got := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
				configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false); got != ownershipUnowned {
				t.Fatalf("verdict %d, want ownershipUnowned", got)
			}
			if b.diags.HasErrors() {
				t.Errorf("declared_untagged = %s became an error:\n%s", verb, renderDiags(b.diags))
			}
		})
	}

	t.Run("AWS tag surface is unchanged", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate})
		if got := b.checkOwnership(k8sConfigMapAddr(t), "aws_cloudwatch_log_group", "/somebody/logs",
			tagSurfaceSchema(), tagSurfaceObject(nil), true, false, false); got != ownershipUnowned {
			t.Fatalf("verdict %d, want ownershipUnowned", got)
		}
		if b.diags.HasErrors() {
			t.Errorf("the AWS side changed; the ruling leaves it alone:\n%s", renderDiags(b.diags))
		}
		if !hasDiag(b.diags, SummaryOutsideEstate, "/somebody/logs") {
			t.Errorf("the AWS warning is gone:\n%s", renderDiags(b.diags))
		}
	})

	// kubernetes_manifest already has the server's own answer at plan
	// time: the dry run (#1081) submits the planned create and turns the
	// 409 into an Error quoting the server. A second refusal ahead of it
	// would replace the server's words with ours.
	t.Run("kubernetes_manifest is left to the dry run", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
		if got := b.checkOwnership(manifestAddr(t), "kubernetes_manifest",
			"apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab",
			manifestTypeSchema(), k8sLiveManifest(t, k8sOwnershipEstate, nil), true, false, false); got != ownershipUnowned {
			t.Fatalf("verdict %d, want ownershipUnowned", got)
		}
		if b.diags.HasErrors() {
			t.Errorf("the manifest surface raised the new error:\n%s", renderDiags(b.diags))
		}
	})

	t.Run("another estate's label keeps its own refusal", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
			configMapTypeSchema(), k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}), true, false, false)
		if len(errorDiagsWith(b.diags, conflictSummary)) != 0 {
			t.Errorf("an object labelled for another estate read as unlabelled:\n%s", renderDiags(b.diags))
		}
	})

	// A record-first read binds whatever key the record names. When that is
	// not the key this block's configuration computes, the object read is
	// at some other name, and a create at the declared one would not
	// collide with it: the old warning stands. When the two agree, the
	// record-first read is reading the declared key and the error applies.
	t.Run("a record naming another key is not the declared key", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnershipAt(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/old-name",
			configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, true, false)
		if b.diags.HasErrors() {
			t.Errorf("an object at a record's stale key raised the declared-key error:\n%s", renderDiags(b.diags))
		}
		if !hasDiag(b.diags, SummaryOutsideEstate, "smoke-k8s/old-name") {
			t.Errorf("the object at the record's key is no longer reported:\n%s", renderDiags(b.diags))
		}
	})
	t.Run("a record naming the declared key is", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnershipAt(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
			configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, true, true)
		if len(errorDiagsWith(b.diags, conflictSummary)) != 1 {
			t.Errorf("a record-first read at the declared key did not refuse:\n%s", renderDiags(b.diags))
		}
	})

	// hashicorp/kubernetes's *_default_* types adopt the object the
	// cluster already made rather than creating one: the create of a
	// kubernetes_default_service_account waits for the namespace's
	// "default" ServiceAccount and updates it. That create never meets a
	// 409, so the ruling's first half does not hold for it, and refusing
	// it would refuse a configuration that works. The same
	// "<provider>_default_" adopter convention internal/live/discovery
	// keys aws_default_* on.
	for _, typ := range []string{"kubernetes_default_service_account", "kubernetes_default_service_account_v1"} {
		t.Run(typ+" adopts on create", func(t *testing.T) {
			b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
			b.checkOwnership(k8sConfigMapAddr(t), typ, "smoke-k8s/default",
				configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false)
			if b.diags.HasErrors() {
				t.Errorf("%s was refused, but its create adopts the existing object and never meets a 409:\n%s", typ, renderDiags(b.diags))
			}
		})
	}

	t.Run("an undeclared object is not declared_untagged", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
			configMapTypeSchema(), k8sLiveConfigMap(nil), false, false, false)
		if b.diags.HasErrors() {
			t.Errorf("an undeclared object raised the declared-quadrant error:\n%s", renderDiags(b.diags))
		}
	})
}

// k8sConflictCluster is the smallest fake API server a kubernetes_config_map
// can be projected through end to end: objects keyed by the provider's own
// import id, namespace/name.
type k8sConflictCluster struct {
	mu      sync.Mutex
	objects map[string]cty.Value
}

func (c *k8sConflictCluster) provider() (addrs.AbsProviderConfig, *tofu.MockProvider) {
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{configMapTestType: configMapTypeSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		obj, ok := c.objects[r.Target.ID]
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: obj}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		id := r.PriorState.GetAttr("id").AsString()
		obj, ok := c.objects[id]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	return provAddr, p
}

// k8sAwsAuth is corpus-eks-basic's shape: the aws-auth ConfigMap EKS
// creates in kube-system, declared by the configuration, carrying no
// tofu-estate label because nothing in this estate ever wrote one.
func k8sAwsAuth(labels map[string]string) cty.Value {
	labelVal := cty.NullVal(cty.Map(cty.String))
	if labels != nil {
		vals := map[string]cty.Value{}
		for k, v := range labels {
			vals[k] = cty.StringVal(v)
		}
		labelVal = cty.MapVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"data":      cty.MapVal(map[string]cty.Value{"mapRoles": cty.StringVal("- rolearn: arn:aws:iam::111111111111:role/node\n")}),
		"id":        cty.StringVal("kube-system/aws-auth"),
		"immutable": cty.NullVal(cty.Bool),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": cty.NullVal(cty.Map(cty.String)),
			"labels":      labelVal,
			"name":        cty.StringVal("aws-auth"),
			"namespace":   cty.StringVal("kube-system"),
		})}),
	})
}

const k8sAwsAuthConfig = `
resource "kubernetes_config_map" "aws_auth" {
  metadata {
    name      = "aws-auth"
    namespace = "kube-system"
  }
  data = {
    mapRoles = "- rolearn: arn:aws:iam::111111111111:role/node\n"
  }
}
`

// TestK8sConflict_awsAuthThroughBuild runs the three acceptance lines
// through [BuildWith], the entry point the live plan and a plain plan under
// a live block both call, so the verdict is the one an operator gets and not
// only the one checkOwnership computes in isolation.
func TestK8sConflict_awsAuthThroughBuild(t *testing.T) {
	const id = "kube-system/aws-auth"
	run := func(t *testing.T, own *Ownership, objects map[string]cty.Value) (*Result, tfdiags.Diagnostics, addrs.AbsResourceInstance) {
		t.Helper()
		cfg := refSeedConfig(t, k8sAwsAuthConfig)
		addr := mustAddr(t, `kubernetes_config_map.aws_auth`)
		cluster := &k8sConflictCluster{objects: objects}
		provAddr, p := cluster.provider()
		res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
			{Addr: addr, Class: identity.ClassConcrete, ImportID: id},
		}, SingleProvider(provAddr, p), Options{Ownership: own})
		return res, diags, addr
	}

	t.Run("unlabelled and read: refused with an error", func(t *testing.T) {
		res, diags, addr := run(t, &Ownership{Estate: policyEstate}, map[string]cty.Value{id: k8sAwsAuth(nil)})
		if !diags.HasErrors() {
			t.Fatalf("the run has no error, so it exits 0 and plans a create the API server answers with 409:\n%s", renderDiags(diags))
		}
		if len(errorDiagsWith(diags, conflictSummary)) != 1 {
			t.Fatalf("want one Error %q naming the object:\n%s", conflictSummary, renderDiags(diags))
		}
		for _, want := range []string{id, addr.String()} {
			if !strings.Contains(errorDiagsWith(diags, conflictSummary)[0].Description().Detail, want) {
				t.Errorf("the error does not name %s:\n%s", want, renderDiags(diags))
			}
		}
		if res != nil && res.Has(addr) {
			t.Errorf("the unlabelled object entered the prior state")
		}
	})

	t.Run("unlabelled and read, under adopt: adopted", func(t *testing.T) {
		res, diags, addr := run(t, &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "adopt")},
			map[string]cty.Value{id: k8sAwsAuth(nil)})
		assertNoErrors(t, diags)
		if !res.Has(addr) {
			t.Fatalf("declared_untagged = adopt did not bring the object into the prior state:\n%s", res)
		}
	})

	t.Run("absent: the create is planned as before", func(t *testing.T) {
		res, diags, addr := run(t, &Ownership{Estate: policyEstate}, nil)
		assertNoErrors(t, diags)
		if res.Has(addr) {
			t.Fatalf("an absent object is in the prior state")
		}
		if om := omissionFor(t, res, addr.String()); om.Reason != ReasonAbsent {
			t.Errorf("omitted as %s, want %s: an absence must never trigger the refusal", om.Reason, ReasonAbsent)
		}
	})

	t.Run("labelled for this estate: admitted", func(t *testing.T) {
		res, diags, addr := run(t, &Ownership{Estate: policyEstate},
			map[string]cty.Value{id: k8sAwsAuth(map[string]string{markers.TagEstate: policyEstate})})
		assertNoErrors(t, diags)
		if !res.Has(addr) {
			t.Fatalf("this estate's own object was not admitted:\n%s", res)
		}
	})
}

// TestK8sConflict_recordFirstKeyThroughBuild pins [wanted.declaredKey]'s
// wiring in [builder.materializeFromRecord]: a record naming the declared
// key reads that key and is refused; a record naming another key reads an
// object a create at the declared key would not collide with, and keeps
// the warning.
func TestK8sConflict_recordFirstKeyThroughBuild(t *testing.T) {
	const declared = "kube-system/aws-auth"
	for name, tc := range map[string]struct {
		recorded  string
		wantError bool
	}{
		"record at the declared key": {declared, true},
		"record at another key":      {"kube-system/aws-auth-old", false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := refSeedConfig(t, k8sAwsAuthConfig)
			addr := mustAddr(t, `kubernetes_config_map.aws_auth`)

			located := newTestLocatedStore(localHintStore(t), policyEstate)
			if _, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: tc.recorded}, ""); err != nil {
				t.Fatalf("seeding the record: %s", err)
			}
			obj := k8sAwsAuth(nil)
			if tc.recorded != declared {
				obj = cty.ObjectVal(map[string]cty.Value{
					"data":      obj.GetAttr("data"),
					"id":        cty.StringVal(tc.recorded),
					"immutable": obj.GetAttr("immutable"),
					"metadata":  obj.GetAttr("metadata"),
				})
			}
			cluster := &k8sConflictCluster{objects: map[string]cty.Value{tc.recorded: obj}}
			provAddr, p := cluster.provider()
			_, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: addr, Class: identity.ClassConcrete, ImportID: declared},
			}, SingleProvider(provAddr, p), Options{
				RecordStore: located.rs,
				Ownership:   &Ownership{Estate: policyEstate},
			})

			got := len(errorDiagsWith(diags, conflictSummary)) == 1
			if got != tc.wantError {
				t.Errorf("refused = %v, want %v:\n%s", got, tc.wantError, renderDiags(diags))
			}
		})
	}
}
