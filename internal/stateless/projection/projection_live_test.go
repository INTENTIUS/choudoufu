// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	goPlugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/logging"
	tfplugin "github.com/opentofu/opentofu/internal/plugin"
	tfplugin6 "github.com/opentofu/opentofu/internal/plugin6"
	"github.com/opentofu/opentofu/internal/plugins"
	"github.com/opentofu/opentofu/internal/providers"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This file holds the one integration test for the projection builder: a
// real AWS provider plugin, a real (emulated) cloud, and no mocks
// anywhere. It is gated on TF_FLOCI_TEST=1 because it needs Docker, a
// terraform binary, and about a minute.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/projection/ -run TestBuildAgainstFloci -v
//
// The shape of the test is the roadmap's "adopt" step, done in process:
// stand the estate up with stock terraform and a plain state file, delete
// the state file, and then rebuild what OpenTofu knows purely by asking
// the cloud.

const (
	// flociPort is this task's port. 4566/4598/4599/4699 are claimed by
	// neighbouring suites and 4601 by the e2e harness, so P1.3 uses 4603.
	flociPort = "4603"

	// terraformBin stands the estate up. Stock terraform is used on
	// purpose: the projection has to recover an estate it did not create.
	terraformBin = "terraform"
)

func TestBuildAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "projection")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	endpoint := "http://localhost:" + flociPort

	flocitest.StartFloci(t, "tofu-stateless-p13", flociPort)

	// The plugin process inherits this environment, which is how it finds
	// floci instead of real AWS.
	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	dir := flocitest.CopyEstate(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	t.Cleanup(func() {
		// Best-effort teardown of the cloud objects. The container goes
		// away regardless, so a failure here is not worth failing on.
		cmd := exec.Command(terraformBin, "destroy", "-auto-approve", "-input=false", "-no-color")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("terraform destroy (best effort) failed: %v\n%s", err, out)
		}
	})

	// The non-event the whole branch is about: delete the state and carry
	// on as though it had never existed.
	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply did not leave a state file where one was expected: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider, providerSchema := launchAWSProvider(t, dir)

	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg)

	provs := SingleProvider(awsProvider, provider)

	start := time.Now()
	res, diags := Build(ctx, cfg, resolutions, provs)
	elapsed := time.Since(start)
	t.Logf("projection built in %s: %d materialized, %d omitted", elapsed, len(res.Materialized), len(res.Omitted))
	for _, addr := range res.Materialized {
		t.Logf("  MATERIALIZED %s", addr)
	}
	for _, o := range res.Omitted {
		t.Logf("  OMITTED      %s", o)
	}
	assertNoErrors(t, diags)

	// The claim: every client-named instance and every instance whose
	// identity composes from client-named ones is recovered from the live
	// system with no state file in sight.
	assertMaterialized(t, res, []string{
		`aws_cloudwatch_log_group.app`,
		`aws_cloudwatch_log_group.optional[0]`,
		`aws_iam_role.app`,
		`aws_iam_role_policy_attachment.app`,
		`aws_s3_bucket.data`,
		`aws_s3_bucket_policy.data`,
		`aws_ssm_parameter.demo_effect`,
		`aws_ssm_parameter.demo_existence`,
	})

	// Each of those objects has to hold real attributes, not an empty
	// husk: a projection whose objects are empty would plan a diff on
	// everything.
	for _, spec := range []struct{ addr, wantAttr string }{
		{`aws_s3_bucket.data`, "tofu-stateless-e2e-data"},
		{`aws_iam_role.app`, "tofu-stateless-e2e-app"},
		{`aws_cloudwatch_log_group.app`, "/stateless-e2e/app"},
		{`aws_s3_bucket_policy.data`, "AllowAppRoleReadWrite"},
		{`aws_iam_role_policy_attachment.app`, "ReadOnlyAccess"},
	} {
		is := res.State.ResourceInstance(mustAddr(t, spec.addr))
		if is == nil || is.Current == nil {
			t.Errorf("%s is not in the projection", spec.addr)
			continue
		}
		attrs := string(is.Current.AttrsJSON)
		if len(attrs) < 2 {
			t.Errorf("%s has no attributes at all", spec.addr)
			continue
		}
		if !strings.Contains(attrs, spec.wantAttr) {
			t.Errorf("%s does not carry %q in its attributes:\n%s", spec.addr, spec.wantAttr, attrs)
		}
		if is.Current.SchemaVersion != uint64(providerSchema.ResourceTypes[mustAddr(t, spec.addr).Resource.Resource.Type].Version) {
			t.Errorf("%s was stored with the wrong schema version", spec.addr)
		}
	}

	// The parent-derived row cannot materialize in phase 1: its parents
	// are the server-assigned types that only marker discovery can find.
	// The point of asserting it here is that the projection says so, in
	// as many words, instead of quietly dropping them.
	assertOmitted(t, res, map[string]Reason{
		`aws_vpc.main`:                          ReasonNeedsDiscovery,
		`aws_subnet.this["a"]`:                  ReasonNeedsDiscovery,
		`aws_subnet.this["b"]`:                  ReasonNeedsDiscovery,
		`aws_security_group.main`:               ReasonNeedsDiscovery,
		`aws_route_table.main`:                  ReasonNeedsDiscovery,
		`aws_internet_gateway.main`:             ReasonNeedsDiscovery,
		`aws_eip.pool[0]`:                       ReasonNeedsDiscovery,
		`aws_eip.pool[1]`:                       ReasonNeedsDiscovery,
		`aws_eip.pool[2]`:                       ReasonNeedsDiscovery,
		`aws_route.internet_gateway`:            ReasonParentUnavailable,
		`aws_route_table_association.this["a"]`: ReasonParentUnavailable,
		`aws_route_table_association.this["b"]`: ReasonParentUnavailable,
	})
	for _, addr := range []string{
		`aws_route.internet_gateway`,
		`aws_route_table_association.this["a"]`,
		`aws_route_table_association.this["b"]`,
	} {
		o := omissionFor(t, res, addr)
		if !strings.Contains(o.Detail, "aws_route_table.main") && !strings.Contains(o.Detail, "aws_subnet.this") {
			t.Errorf("%s is omitted without naming the parent that blocked it:\n%s", addr, o.Detail)
		}
	}

	// No file writes ever: the projection must not have put anything back
	// where the state file was.
	if _, err := os.Stat(stateFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a state file exists after building a projection (err = %v); this package must never write one", err)
	}
}

// ---------------------------------------------------------------------------
// floci
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// The real provider plugin
// ---------------------------------------------------------------------------

// launchAWSProvider starts the AWS provider plugin that terraform init
// downloaded into the work directory and configures it the way the
// estate's provider block does.
//
// The plumbing here is deliberately the same as internal/command's
// providerFactory: go-plugin launches the executable, the versioned plugin
// set negotiates protocol 5 or 6, and internal/plugins configures it.
// P1.4's command will get all of this from the Meta it already has; a test
// has to spell it out.
func launchAWSProvider(t *testing.T, dir string) (providers.Interface, providers.GetProviderSchemaResponse) {
	t.Helper()

	exe := findProviderBinary(t, dir)
	t.Logf("using provider plugin %s", exe)

	awsAddr := addrs.NewDefaultProvider("aws")
	factories := plugins.ProviderFactories{awsAddr: pluginFactory(exe)}
	lib := plugins.NewLibrary(factories, nil)
	mgr := lib.NewProviderManager()
	t.Cleanup(func() {
		if err := mgr.CloseAll(context.Background()); err != nil {
			t.Logf("closing the provider: %v", err)
		}
		goPlugin.CleanupClients()
	})

	// The schema has to be read before the provider is configured, because
	// the configuration value has to conform to it.
	schema, diags := mgr.GetProviderSchema(context.Background(), awsAddr)
	if diags.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", diags.Err())
	}

	cfgVal := decodeProviderConfig(t, schema)

	provider, diags := mgr.NewConfiguredProvider(context.Background(), awsAddr, cfgVal)
	if diags.HasErrors() {
		t.Fatalf("configuring the AWS provider: %s", diags.Err())
	}
	return provider, schema
}

// decodeProviderConfig builds the provider configuration value from the
// same settings the estate's provider block carries. Everything else -
// region, credentials, and the floci endpoint - reaches the plugin through
// the environment, exactly as it does when the estate is applied by
// terraform.
func decodeProviderConfig(t *testing.T, schema providers.GetProviderSchemaResponse) cty.Value {
	t.Helper()

	const src = `
skip_credentials_validation = true
skip_metadata_api_check     = true
s3_use_path_style           = true
`
	body, hclDiags := hclsyntax.ParseConfig([]byte(src), "provider.hcl", hcl.Pos{Line: 1, Column: 1})
	if hclDiags.HasErrors() {
		t.Fatalf("parsing the provider configuration: %s", hclDiags.Error())
	}
	val, hclDiags := hcldec.Decode(body.Body, schema.Provider.Block.DecoderSpec(), nil)
	if hclDiags.HasErrors() {
		t.Fatalf("decoding the provider configuration against the provider's schema: %s", hclDiags.Error())
	}
	return val
}

// findProviderBinary locates the plugin executable terraform init unpacked
// under the work directory.
func findProviderBinary(t *testing.T, dir string) string {
	t.Helper()

	var found []string
	root := filepath.Join(dir, ".terraform", "providers")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "terraform-provider-aws") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&0o111 == 0 {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		t.Fatalf("looking for the provider plugin under %s: %v", root, err)
	}
	if len(found) == 0 {
		t.Fatalf("terraform init did not leave an AWS provider plugin under %s", root)
	}
	sort.Strings(found)
	return found[0]
}

// pluginFactory is internal/command's providerFactory, minus the
// providercache lookup: go-plugin launches the given executable and
// dispenses whichever protocol version it negotiated.
func pluginFactory(exe string) providers.Factory {
	schemaCache := providers.NewSchemaCache()

	return func() (providers.Interface, error) {
		config := &goPlugin.ClientConfig{
			HandshakeConfig:  tfplugin.Handshake,
			Logger:           logging.NewProviderLogger(""),
			AllowedProtocols: []goPlugin.Protocol{goPlugin.ProtocolGRPC},
			Managed:          true,
			Cmd:              exec.Command(exe), //nolint:gosec // the path comes from terraform init in a temp dir
			AutoMTLS:         true,
			VersionedPlugins: tfplugin.VersionedPlugins,
			SyncStdout:       logging.PluginOutputMonitor("aws:stdout"),
			SyncStderr:       logging.PluginOutputMonitor("aws:stderr"),
		}

		client := goPlugin.NewClient(config)
		rpcClient, err := client.Client()
		if err != nil {
			return nil, err
		}
		raw, err := rpcClient.Dispense(tfplugin.ProviderPluginName)
		if err != nil {
			return nil, err
		}

		switch protoVer := client.NegotiatedVersion(); protoVer {
		case 5:
			p := raw.(*tfplugin.GRPCProvider)
			p.PluginClient = client
			p.SchemaCache = schemaCache
			return p, nil
		case 6:
			p := raw.(*tfplugin6.GRPCProvider)
			p.PluginClient = client
			p.SchemaCache = schemaCache
			return p, nil
		default:
			return nil, fmt.Errorf("the AWS provider negotiated unsupported plugin protocol version %d", protoVer)
		}
	}
}
