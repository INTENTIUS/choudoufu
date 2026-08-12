// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package listclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/logging"
	"github.com/opentofu/opentofu/internal/plugin"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/stateless/listclient"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This is the live half of P2.2: the same client, pointed at the real AWS
// provider binary the fixture pins, listing real resources out of the floci
// emulator. It proves the wire shape, which no amount of fake-server testing
// can: that the ListResource method name, the request fields and the
// identity encoding are what an actual provider serves.
//
// It is gated on TF_FLOCI_TEST=1 because it needs Docker, the network (to
// install the provider) and about a minute.

const (
	flociPort = "4604"
	awsRegion = "us-east-1"
	// terraform, not tofu: the fixture is installed with the same tool the
	// harness uses, and this repo's own binary is what is under test.
	terraformBin = "terraform"
	awsBin       = "aws"
)

func TestIntegrationListAWSVPCs(t *testing.T) {
	flocitest.Gate(t, "list client")
	flocitest.SkipWithoutBinary(t, terraformBin)
	flocitest.SkipWithoutBinary(t, awsBin)
	flocitest.SkipWithoutBinary(t, "docker")

	endpoint := "http://localhost:" + flociPort
	awsEnv := []string{
		"AWS_ENDPOINT_URL=" + endpoint,
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_REGION=" + awsRegion,
	}
	// Also set them on this process, not only on the children built from
	// os.Environ() + awsEnv. This test starts its own container on its own
	// port and every AWS call it makes must go there, but an operator with
	// AWS_ENDPOINT_URL already exported - pointing at whatever emulator they
	// were last working against - inherits that value through the provider
	// plugin's environment, and the test then talks to somebody else's cloud
	// and fails in a way that has nothing to do with what it tests.
	// t.Setenv also restores the previous values when the test ends.
	for _, kv := range awsEnv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	t.Setenv("AWS_DEFAULT_REGION", awsRegion)
	// The variables that would route somewhere else by another road, removed
	// rather than blanked: AWS_PROFILE="" is a profile NAMED "", and the CLI
	// refuses with "The config profile () could not be found" - the empty
	// string is not how you say "no profile" to any of these.
	unsetEnv(t, "AWS_PROFILE", "AWS_SESSION_TOKEN", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE")

	flocitest.StartFloci(t, "listclient-floci", flociPort)
	providerExe := installAWSProvider(t, awsEnv)

	// Two VPCs with distinct tags, created out of band so that nothing
	// about this test depends on OpenTofu's own apply path working.
	runID := fmt.Sprintf("p22-%d", time.Now().UnixNano())
	vpcA := createVPC(t, awsEnv, "10.90.0.0/16", runID, "alpha")
	vpcB := createVPC(t, awsEnv, "10.91.0.0/16", runID, "beta")
	t.Logf("created VPCs: %s (alpha), %s (beta)", vpcA, vpcB)

	provider := launchProvider(t, providerExe, awsEnv)
	configureAWSProvider(t, provider)

	ctx := context.Background()

	// --- ListSchemas ---------------------------------------------------
	schemas, diags := listclient.ListSchemas(ctx, provider)
	if diags.HasErrors() {
		t.Fatalf("ListSchemas: %s", diags.Err())
	}
	t.Logf("provider reports %d listable types", schemas.Len())
	if !schemas.Supports("aws_vpc") {
		t.Fatalf("aws_vpc is not listable; provider lists %d types", schemas.Len())
	}
	for _, name := range []string{"aws_subnet", "aws_security_group", "aws_eip", "aws_route_table", "aws_internet_gateway"} {
		if !schemas.Supports(name) {
			t.Errorf("fixture type %q is not listable", name)
		}
	}

	vpcSchema, _ := schemas.Get("aws_vpc")
	if !vpcSchema.HasIdentity() {
		t.Fatal("aws_vpc has no identity schema, so list results carry no identity")
	}
	t.Logf("aws_vpc list config arguments: %v", sortedNames(vpcSchema.Config.ImpliedType().AttributeTypes()))
	t.Logf("aws_vpc identity attributes: %v", sortedAttrNames(vpcSchema.Identity.Attributes))

	// --- List, unfiltered ----------------------------------------------
	config, diags := vpcSchema.BuildConfig(map[string]cty.Value{
		"region": cty.StringVal(awsRegion),
	})
	if diags.HasErrors() {
		t.Fatalf("BuildConfig: %s", diags.Err())
	}

	results, diags := listclient.List(ctx, provider, "aws_vpc", config, false)
	if diags.HasErrors() {
		t.Fatalf("List(aws_vpc): %s", diags.Err())
	}
	t.Logf("unfiltered list returned %d VPCs", len(results))

	found := map[string]listclient.Result{}
	for _, r := range results {
		id, ok := r.IdentityAttr("id")
		if !ok {
			t.Errorf("a result carried no id in its identity: %#v", r.Identity)
			continue
		}
		found[id] = r
	}
	for _, want := range []string{vpcA, vpcB} {
		r, ok := found[want]
		if !ok {
			t.Errorf("VPC %s is missing from the list results (got %v)", want, sortedKeys(found))
			continue
		}
		if !r.HasIdentity() {
			t.Errorf("VPC %s came back without an identity", want)
		}
		if region, ok := r.IdentityAttr("region"); !ok || region != awsRegion {
			t.Errorf("VPC %s identity region: %q (present=%v)", want, region, ok)
		}
		t.Logf("VPC %s: display_name=%q identity=%s", want, r.DisplayName, r.Identity.GoString())
	}

	// --- List with the full resource object ----------------------------
	withObjects, diags := listclient.List(ctx, provider, "aws_vpc", config, true)
	if diags.HasErrors() {
		t.Errorf("List(aws_vpc, include_resource_object): %s", diags.Err())
	} else {
		var checked bool
		for _, r := range withObjects {
			id, _ := r.IdentityAttr("id")
			if id != vpcA {
				continue
			}
			checked = true
			if r.Resource == cty.NilVal {
				t.Error("include_resource_object was requested but no object came back")
				break
			}
			tags := r.Resource.GetAttr("tags")
			if tags.IsNull() {
				t.Error("the resource object carried no tags, so markers cannot be read from it")
				break
			}
			got := tags.Index(cty.StringVal("listclient-run"))
			if got.IsNull() || got.AsString() != runID {
				t.Errorf("resource object tags do not carry the run tag: %s", tags.GoString())
			}
		}
		if !checked {
			t.Error("the object-carrying list did not include the VPC we made")
		}
	}

	// --- Server-side tag filtering -------------------------------------
	// aws_vpc's list schema offers EC2-style filter blocks. Whether the
	// emulator honours a tag filter is a property of floci, not of this
	// client, so this reports rather than asserts on the filtering itself.
	filterTy := vpcSchema.Config.ImpliedType().AttributeTypes()["filter"]
	if filterTy == cty.NilType {
		t.Log("SERVER-SIDE FILTER: aws_vpc's list schema has no filter argument; P2.3 must filter client-side")
		return
	}

	filtered, diags := vpcSchema.BuildConfig(map[string]cty.Value{
		"region": cty.StringVal(awsRegion),
		"filter": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":   cty.StringVal("tag:listclient-name"),
			"values": cty.ListVal([]cty.Value{cty.StringVal("alpha")}),
		})}),
	})
	if diags.HasErrors() {
		t.Fatalf("BuildConfig with filter: %s", diags.Err())
	}

	filteredResults, diags := listclient.List(ctx, provider, "aws_vpc", filtered, false)
	if diags.HasErrors() {
		t.Errorf("SERVER-SIDE FILTER: the filtered list failed: %s", diags.Err())
		return
	}
	var ids []string
	for _, r := range filteredResults {
		if id, ok := r.IdentityAttr("id"); ok {
			ids = append(ids, id)
		}
	}
	switch {
	case len(filteredResults) == 1 && ids[0] == vpcA:
		t.Logf("SERVER-SIDE FILTER: WORKS — tag:listclient-name=alpha returned exactly %s", vpcA)
	case len(filteredResults) >= len(results):
		t.Logf("SERVER-SIDE FILTER: IGNORED — the filter returned %d of %d VPCs; P2.3 must filter client-side",
			len(filteredResults), len(results))
	default:
		t.Logf("SERVER-SIDE FILTER: PARTIAL — expected only %s, got %v", vpcA, ids)
	}
}

// unsetEnv removes variables for the duration of a test and puts back
// whatever was there. t.Setenv is no help here: setting one of these to the
// empty string is a different thing from not setting it at all.
func unsetEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, had := os.LookupEnv(name)
		if !had {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
		t.Cleanup(func() {
			if err := os.Setenv(name, old); err != nil {
				t.Logf("restoring %s: %v", name, err)
			}
		})
	}
}

// installAWSProvider copies the estate fixture somewhere scratch, runs
// terraform init in it, and returns the path of the installed provider
// binary. Going through init is what pins the version to the fixture's
// rather than to whatever this test would otherwise guess.
func installAWSProvider(t *testing.T, awsEnv []string) string {
	t.Helper()

	estate, err := filepath.Abs(filepath.Join("..", "..", "..", "stateless", "e2e", "estate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(estate); err != nil {
		t.Fatalf("estate fixture not found: %v", err)
	}

	dir := t.TempDir()
	if out, err := exec.Command("cp", "-R", estate+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("copying the estate fixture: %v: %s", err, out)
	}

	cmd := exec.Command(terraformBin, "init", "-no-color", "-input=false")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), awsEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform init: %v\n%s", err, out)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".terraform", "providers", "*", "hashicorp", "aws", "*", "*", "terraform-provider-aws*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one installed aws provider binary, got %v", matches)
	}
	t.Logf("provider binary: %s", matches[0])
	return matches[0]
}

// launchProvider runs the provider binary through this repo's own plugin
// machinery, which is the point: the handshake, the protocol negotiation and
// the client stubs under test are the real ones.
func launchProvider(t *testing.T, exe string, awsEnv []string) *plugin.GRPCProvider {
	t.Helper()

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), awsEnv...)

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  plugin.Handshake,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		VersionedPlugins: plugin.VersionedPlugins,
		Managed:          true,
		Cmd:              cmd,
		// The repo's own provider logger, so plugin chatter obeys TF_LOG
		// instead of drowning the test output unconditionally.
		Logger: logging.NewProviderLogger(""),
	})
	t.Cleanup(client.Kill)

	rpcClient, err := client.Client()
	if err != nil {
		t.Fatalf("starting the provider: %v", err)
	}
	raw, err := rpcClient.Dispense(plugin.ProviderPluginName)
	if err != nil {
		t.Fatalf("dispensing the provider: %v", err)
	}

	t.Logf("negotiated plugin protocol version %d", client.NegotiatedVersion())
	p, ok := raw.(*plugin.GRPCProvider)
	if !ok {
		t.Fatalf("expected a protocol 5 provider handle, got %T", raw)
	}
	// providerFactory in internal/command does the same two things after
	// dispensing; without a schema cache the handle panics on first use.
	p.PluginClient = client
	p.SchemaCache = providers.NewSchemaCache()
	return p
}

// configureAWSProvider sends the same provider configuration the fixture
// uses: everything null except the flags an emulator needs. The endpoint and
// credentials arrive through the environment, which the plugin subprocess
// inherits.
func configureAWSProvider(t *testing.T, p *plugin.GRPCProvider) {
	t.Helper()

	schema := p.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("GetProviderSchema: %s", schema.Diagnostics.Err())
	}

	// TypeSchema.BuildConfig fills every unset argument from the schema,
	// which is exactly what is wanted for a schema this large.
	providerBlock := listclient.TypeSchema{TypeName: "provider", Config: schema.Provider.Block}
	config, diags := providerBlock.BuildConfig(map[string]cty.Value{
		"skip_credentials_validation": cty.True,
		"skip_requesting_account_id":  cty.True,
		"skip_metadata_api_check":     cty.True,
		"s3_use_path_style":           cty.True,
		"region":                      cty.StringVal(awsRegion),
	})
	if diags.HasErrors() {
		t.Fatalf("building the provider configuration: %s", diags.Err())
	}

	resp := p.ConfigureProvider(context.Background(), providers.ConfigureProviderRequest{
		TerraformVersion: "1.6.0",
		Config:           config,
	})
	if resp.Diagnostics.HasErrors() {
		t.Fatalf("ConfigureProvider: %s", resp.Diagnostics.Err())
	}
}

// createVPC makes a VPC out of band and returns its id.
func createVPC(t *testing.T, awsEnv []string, cidr, runID, name string) string {
	t.Helper()

	cmd := exec.Command(awsBin, "ec2", "create-vpc",
		"--cidr-block", cidr,
		"--tag-specifications",
		fmt.Sprintf("ResourceType=vpc,Tags=[{Key=listclient-run,Value=%s},{Key=listclient-name,Value=%s}]", runID, name),
		"--output", "json")
	cmd.Env = append(os.Environ(), awsEnv...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("aws ec2 create-vpc: %v: %s", err, out)
	}

	var parsed struct {
		Vpc struct {
			VpcId string `json:"VpcId"`
		} `json:"Vpc"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parsing create-vpc output: %v: %s", err, out)
	}
	if parsed.Vpc.VpcId == "" {
		t.Fatalf("create-vpc returned no id: %s", out)
	}
	return parsed.Vpc.VpcId
}

func sortedNames(m map[string]cty.Type) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func sortedAttrNames[T any](m map[string]T) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func sortedKeys(m map[string]listclient.Result) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
