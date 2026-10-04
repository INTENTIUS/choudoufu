// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1113, build-plan step 1: a provider block whose own
// arguments read a MANAGED value - `host = aws_eks_cluster.this.endpoint`,
// or the terraform-aws-modules/eks v19+ shape `host =
// module.eks.cluster_endpoint` - configures on day 2, from the cluster the
// provider-configuration fixpoint read.
//
// The design comment's reading of the code was that this is red without the
// fix: [dataread.AnalyzeProviderConfigs] collected only the data sources a
// provider block reaches, so nothing demanded the cluster, and
// [projectionProviders.providerConfigValue] decoded the block through an
// evaluator that refuses every managed and module-output reference. The
// sweep then read the cluster as unreachable on an estate whose cluster
// exists. corpus-eks-basic never hit it because v9.0.0 goes through
// data.aws_eks_cluster.
//
// Written under the maintainer's no-testing ruling for this effort and NOT
// run by the change that added it. The fixtures' module outputs use try()
// exactly as the published module does; if the static evaluator lets try()
// swallow a refused reference, the module rows here fail on a null host,
// which is a finding about the evaluator, not about these tests.
//
// The calls are made directly, as [TestProviderWorkOverTargetExcludedBlocks]
// makes them, so a count or a value here is the fixpoint's and the
// provider-configuration decode's, not a whole plan's.

// providerManagedFixtures is every provider-block shape step 1 names, with
// the address of the cluster instance the block reads and what the decoded
// block must carry.
var providerManagedFixtures = []struct {
	fixture string
	cluster string
	// token is the decoded token argument, or "" when the block names none.
	token string
	// exec is true when the credential comes from an exec block.
	exec bool
}{
	{fixture: "live-provider-managed-direct", cluster: "aws_eks_cluster.this", token: "static-token"},
	{fixture: "live-provider-managed-module", cluster: "module.eks.aws_eks_cluster.this[0]", token: "k8s-aws-v1.demo"},
	{fixture: "live-provider-managed-exec", cluster: "module.eks.aws_eks_cluster.this[0]", exec: true},
}

const providerManagedCA = "-----BEGIN CERTIFICATE-----demo"

func TestProviderBlockReadsAManagedValue(t *testing.T) {
	for _, tc := range providerManagedFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := providerManagedLoadConfig(t, filepath.Join("testdata", tc.fixture))
			cloud := newProviderManagedCloud()

			resolutions, resolveDiags := liveResolve(t.Context(), cfg, cloud, nil, nil, nil)
			if n := errorCount(resolveDiags); n != 0 {
				t.Fatalf("liveResolve refused with %d error(s): %v", n, renderDiags(resolveDiags))
			}

			dataResults, managed, readDiags := liveProviderDataReads(t.Context(), cfg, cloud, nil, resolutions, nil, 1, nil, nil)
			if readDiags.HasErrors() {
				t.Fatalf("the provider-configuration fixpoint raised an error: %v", renderDiags(readDiags))
			}
			if _, ok := managed[tc.cluster]; !ok {
				t.Fatalf("the fixpoint did not read %s; it read %v", tc.cluster, providerManagedKeys(managed))
			}
			// Exactly the cluster: the block names three outputs of a call
			// that declares four, and the fourth (cluster_arn) reads the same
			// instance, so a demand that over-reached would still read one
			// instance - the count that would move is a SECOND block's.
			if got := renderCounts(cloud.reads); got != "1 [aws_eks_cluster=1]" {
				t.Errorf("ReadResource calls: got %s, want 1 [aws_eks_cluster=1]", got)
			}

			val := providerManagedDecode(t, cfg, cloud, dataResults, managed)
			if got := val.GetAttr("host"); got.IsNull() || !got.IsKnown() || got.AsString() != "https://demo.example" {
				t.Errorf("host = %#v, want the cluster's live endpoint https://demo.example", got)
			}
			if got := val.GetAttr("cluster_ca_certificate"); got.IsNull() || !got.IsKnown() || got.AsString() != providerManagedCA {
				t.Errorf("cluster_ca_certificate = %#v, want the decoded live CA %q", got, providerManagedCA)
			}
			token, _ := val.GetAttr("token").Unmark()
			switch {
			case tc.token != "" && (token.IsNull() || !token.IsKnown() || token.AsString() != tc.token):
				t.Errorf("token = %#v, want %q", token, tc.token)
			case tc.token == "" && !token.IsNull():
				t.Errorf("token = %#v, want null: this block authenticates through exec", token)
			}
			if tc.exec {
				exec := val.GetAttr("exec")
				if exec.IsNull() || exec.LengthInt() != 1 {
					t.Fatalf("exec = %#v, want one block", exec)
				}
				args := exec.Index(cty.NumberIntVal(0)).GetAttr("args")
				var got []string
				for it := args.ElementIterator(); it.Next(); {
					_, v := it.Element()
					got = append(got, v.AsString())
				}
				if want := "eks get-token --cluster-name demo"; strings.Join(got, " ") != want {
					t.Errorf("exec args = %q, want %q: the cluster name comes through a module output", strings.Join(got, " "), want)
				}
			}
		})
	}
}

// TestProviderBlockReadsAManagedValueFromPriorState is the live-import leg:
// [liveImportProviderDataReads] seeds the state file's values as prior
// managed values, and a provider block reading the cluster directly or
// through a module output is answered from them with no read at all. The
// endpoint the prior value carries differs from the one the cloud would
// answer, so a value that came from anywhere else fails here.
func TestProviderBlockReadsAManagedValueFromPriorState(t *testing.T) {
	for _, tc := range providerManagedFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := providerManagedLoadConfig(t, filepath.Join("testdata", tc.fixture))
			cloud := newProviderManagedCloud()

			resolutions, resolveDiags := liveResolve(t.Context(), cfg, cloud, nil, nil, nil)
			if n := errorCount(resolveDiags); n != 0 {
				t.Fatalf("liveResolve refused with %d error(s): %v", n, renderDiags(resolveDiags))
			}
			prior := map[string]cty.Value{tc.cluster: providerManagedClusterObject("demo", "https://from-state.example")}

			dataResults, managed, readDiags := liveProviderDataReads(t.Context(), cfg, cloud, nil, resolutions, nil, 1, nil, prior)
			if readDiags.HasErrors() {
				t.Fatalf("the provider-configuration fixpoint raised an error: %v", renderDiags(readDiags))
			}
			if n := len(cloud.imports) + len(cloud.reads); n != 0 {
				t.Errorf("the fixpoint read the cluster although the state carried it: imports %s, reads %s",
					renderCounts(cloud.imports), renderCounts(cloud.reads))
			}
			val := providerManagedDecode(t, cfg, cloud, dataResults, managed)
			if got := val.GetAttr("host"); got.IsNull() || !got.IsKnown() || got.AsString() != "https://from-state.example" {
				t.Errorf("host = %#v, want the state's endpoint https://from-state.example", got)
			}
		})
	}
}

// TestProviderBlockOverAClusterNotCreatedYet is the ruling's first half: a
// cluster that does not exist reads as empty, stock's order. The fixpoint
// raises nothing and the provider is not evaluable, which is the condition
// [liveDiscoverProviderUnavailable] already downgrades for a sweep no
// needs-discovery instance depends on.
func TestProviderBlockOverAClusterNotCreatedYet(t *testing.T) {
	for _, tc := range providerManagedFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := providerManagedLoadConfig(t, filepath.Join("testdata", tc.fixture))
			cloud := newProviderManagedCloud()
			cloud.absent = true

			resolutions, _ := liveResolve(t.Context(), cfg, cloud, nil, nil, nil)
			dataResults, managed, readDiags := liveProviderDataReads(t.Context(), cfg, cloud, nil, resolutions, nil, 1, nil, nil)
			if readDiags.HasErrors() {
				t.Fatalf("a cluster that does not exist yet raised an error: %v", renderDiags(readDiags))
			}
			if _, ok := managed[tc.cluster]; ok {
				t.Errorf("the fixpoint holds a value for %s, which does not exist", tc.cluster)
			}

			p := &projectionProviders{config: cfg, providerDataResults: dataResults, providerManagedResults: managed}
			_, diags := p.providerConfigValue(t.Context(), providerManagedKubernetes, providerManagedKubernetesSchema().DecoderSpec())
			if !diags.HasErrors() {
				t.Errorf("the kubernetes provider was configured over a cluster that does not exist")
			}
		})
	}
}

// TestProviderBlockOverAnUnreadableCluster is the ruling's second half: a
// read failure against a cluster that exists is an error, never an empty
// cluster leg.
func TestProviderBlockOverAnUnreadableCluster(t *testing.T) {
	for _, tc := range providerManagedFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := providerManagedLoadConfig(t, filepath.Join("testdata", tc.fixture))
			cloud := newProviderManagedCloud()
			cloud.failRead = true

			resolutions, _ := liveResolve(t.Context(), cfg, cloud, nil, nil, nil)
			_, _, readDiags := liveProviderDataReads(t.Context(), cfg, cloud, nil, resolutions, nil, 1, nil, nil)
			found := false
			for _, d := range readDiags {
				if d.Severity() == tfdiags.Error && d.Description().Summary == summaryProviderConfigManagedReadFailed {
					found = true
					if !strings.Contains(d.Description().Detail, tc.cluster) {
						t.Errorf("the error does not name %s: %s", tc.cluster, d.Description().Detail)
					}
				}
			}
			if !found {
				t.Errorf("a failed read of %s raised no %q error; got %v", tc.cluster, summaryProviderConfigManagedReadFailed, renderDiags(readDiags))
			}
		})
	}
}

// TestProviderBlockManagedDemandIsOfflineAndGated pins the live-import gate
// ([dataread.Analysis.Demands] over bare options) for the three shapes: the
// exec fixture names no data source at all, so a gate that still answered
// for data sources only would skip the phase for it.
func TestProviderBlockManagedDemandIsOfflineAndGated(t *testing.T) {
	for _, tc := range providerManagedFixtures {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := providerManagedLoadConfig(t, filepath.Join("testdata", tc.fixture))
			a := dataread.AnalyzeProviderConfigs(t.Context(), cfg, dataread.Options{})
			if !a.Demands() {
				t.Errorf("Demands() = false for a provider block that reads %s", tc.cluster)
			}
			if len(a.ProviderManagedRefusals()) == 0 {
				t.Errorf("no provider-block managed refusal recorded for %s", tc.cluster)
			}
		})
	}
}

// TestProviderConfigEvaluatorLeavesOtherBlocksAlone: a block that reaches no
// managed value and no module output keeps the evaluator it had before
// #1113. live-target-provider-work's kubernetes block reads only data
// sources, and its aws block is literal.
func TestProviderConfigEvaluatorLeavesOtherBlocksAlone(t *testing.T) {
	cfg := liveTestLoadConfig(t, filepath.Join("testdata", targetWorkFixture))
	for _, pc := range cfg.Module.ProviderConfigs {
		if got := dataread.ProviderConfigEvaluator(t.Context(), cfg, addrs.RootModule, pc, nil, nil); got != nil {
			t.Errorf("provider %q got the live evaluator, but reaches no managed value or module output", pc.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Harness.
// ---------------------------------------------------------------------------

var providerManagedKubernetes = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("kubernetes"),
}

// providerManagedKubernetesSchema is the slice of hashicorp/kubernetes's
// provider schema these fixtures set.
func providerManagedKubernetesSchema() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"host":                   {Type: cty.String, Optional: true},
			"token":                  {Type: cty.String, Optional: true, Sensitive: true},
			"cluster_ca_certificate": {Type: cty.String, Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"exec": {
				Nesting:  configschema.NestingList,
				MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"api_version": {Type: cty.String, Required: true},
					"command":     {Type: cty.String, Required: true},
					"args":        {Type: cty.List(cty.String), Optional: true},
					"env":         {Type: cty.Map(cty.String), Optional: true},
				}},
			},
		},
	}
}

func providerManagedClusterType() cty.Type {
	return cty.Object(map[string]cty.Type{
		"id":                    cty.String,
		"name":                  cty.String,
		"arn":                   cty.String,
		"endpoint":              cty.String,
		"certificate_authority": cty.List(cty.Object(map[string]cty.Type{"data": cty.String})),
	})
}

func providerManagedClusterObject(name, endpoint string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(name),
		"name":     cty.StringVal(name),
		"arn":      cty.StringVal("arn:aws:eks:us-east-1:000000000000:cluster/" + name),
		"endpoint": cty.StringVal(endpoint),
		"certificate_authority": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"data": cty.StringVal(base64.StdEncoding.EncodeToString([]byte(providerManagedCA))),
		})}),
	})
}

// providerManagedCloud is [targetWorkCloud] with an EKS cluster whose live
// object carries an endpoint and a CA, the aws_eks_cluster_auth data source,
// and two failure modes.
type providerManagedCloud struct {
	*targetWorkCloud

	// absent makes every import answer "no such object": a greenfield
	// cluster.
	absent bool
	// failRead makes every ReadResource fail: a cluster that exists and
	// cannot be reached.
	failRead bool
}

func newProviderManagedCloud() *providerManagedCloud {
	c := &providerManagedCloud{targetWorkCloud: newTargetWorkCloud()}

	clusterAttrs := map[string]*configschema.Attribute{}
	for name, ty := range providerManagedClusterType().AttributeTypes() {
		clusterAttrs[name] = &configschema.Attribute{Type: ty, Computed: true}
	}
	clusterAttrs["name"] = &configschema.Attribute{Type: cty.String, Optional: true}
	types := c.aws.GetProviderSchemaResponse.ResourceTypes
	cluster := types["aws_eks_cluster"]
	cluster.Block = &configschema.Block{Attributes: clusterAttrs}
	types["aws_eks_cluster"] = cluster

	c.aws.GetProviderSchemaResponse.DataSources["aws_eks_cluster_auth"] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":    {Type: cty.String, Computed: true},
		"name":  {Type: cty.String, Required: true},
		"token": {Type: cty.String, Computed: true, Sensitive: true},
	}}}
	c.kubernetes.GetProviderSchemaResponse.Provider = providers.Schema{Block: providerManagedKubernetesSchema()}

	importInner := c.aws.ImportResourceStateFn
	c.aws.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if c.absent {
			c.count(c.imports, req.TypeName)
			return providers.ImportResourceStateResponse{}
		}
		return importInner(req)
	}
	readInner := c.aws.ReadResourceFn
	c.aws.ReadResourceFn = func(req providers.ReadResourceRequest) (resp providers.ReadResourceResponse) {
		if req.TypeName != "aws_eks_cluster" {
			return readInner(req)
		}
		c.count(c.reads, req.TypeName)
		if c.failRead {
			resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(tfdiags.Error, "connection refused", "dial tcp: the EKS API is unreachable"))
			return resp
		}
		id := req.PriorState.GetAttr("id").AsString()
		resp.NewState = providerManagedClusterObject(id, "https://"+id+".example")
		return resp
	}
	dataInner := c.aws.ReadDataSourceFn
	c.aws.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) (resp providers.ReadDataSourceResponse) {
		if req.TypeName != "aws_eks_cluster_auth" {
			return dataInner(req)
		}
		c.count(c.dataReads, req.TypeName)
		name := req.Config.GetAttr("name")
		resp.State = cty.ObjectVal(map[string]cty.Value{
			"id":    name,
			"name":  name,
			"token": cty.StringVal("k8s-aws-v1." + name.AsString()),
		})
		return resp
	}
	return c
}

// providerManagedDecode decodes the root kubernetes block the way
// ConfiguredProvider does, from the fixpoint's own two result maps.
func providerManagedDecode(t *testing.T, cfg *configs.Config, _ *providerManagedCloud, dataResults, managed map[string]cty.Value) cty.Value {
	t.Helper()
	p := &projectionProviders{config: cfg, providerDataResults: dataResults, providerManagedResults: managed}
	val, diags := p.providerConfigValue(t.Context(), providerManagedKubernetes, providerManagedKubernetesSchema().DecoderSpec())
	if diags.HasErrors() {
		t.Fatalf("the kubernetes provider block did not decode: %v", renderDiags(diags))
	}
	return val
}

func providerManagedKeys(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// providerManagedLoadConfig is [liveTestLoadConfig] for a fixture that
// calls a local module, resolved relative to the calling module's own
// directory.
func providerManagedLoadConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	dirs := map[string]string{"": dir}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, call, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			if req.SourceAddr == nil {
				return nil, nil, nil
			}
			sourcePath := filepath.Join(dirs[req.Parent.Path.String()], req.SourceAddr.String())
			dirs[req.Path.String()] = sourcePath
			child, modDiags := parser.LoadConfigDir(sourcePath)
			return child, nil, modDiags
		}, parser.LoadSymbolFilesInDir,
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}
