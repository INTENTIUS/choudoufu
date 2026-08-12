// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
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
	"github.com/opentofu/opentofu/internal/stateless/listclient"
	"github.com/opentofu/opentofu/internal/stateless/markers"
	"github.com/opentofu/opentofu/internal/stateless/projection"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This is P2.3's live half: a real AWS provider plugin, the emulated cloud,
// the estate stood up by stock terraform, its state file deleted, and then
// the whole of the estate recovered from tags alone.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/discovery/ -run TestDiscoverAgainstFloci -v
//
// It is gated because it needs Docker, a terraform binary and about a
// minute.

const (
	awsRegion = "us-east-1"

	// terraformBin stands the estate up. Stock terraform on purpose:
	// discovery has to recover an estate it did not create.
	terraformBin = "terraform"
)

func TestDiscoverAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-p23")
	endpoint := "http://localhost:" + flociPort

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := flocitest.CopyEstate(t)
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	// The non-event: delete the state and carry on as though it had never
	// existed.
	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)

	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	// --- What the markers actually look like on the wire ------------------
	//
	// Where the tags live is per-type knowledge this package has to be right
	// about, so it is checked against the real provider rather than assumed:
	// every EC2 type in the v0 subset carries them in a top-level "tags"
	// attribute of the listed resource object.
	logMarkerAccessPaths(ctx, t, provider)

	// --- Discovery --------------------------------------------------------
	start := time.Now()
	res, diags := Discover(ctx, Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    provider,
		Region:      awsRegion,
	})
	elapsed := time.Since(start)
	t.Logf("discovery took %s\n%s", elapsed, res)
	assertNoErrors(t, diags)

	for _, addr := range allDiscovered {
		if _, ok := res.BindingFor(mustAddr(t, addr)); !ok {
			t.Errorf("%s did not bind to a live resource", addr)
		}
	}
	if len(res.Bindings) != len(allDiscovered) {
		t.Errorf("bound %d instances, want the fixture's %d", len(res.Bindings), len(allDiscovered))
	}
	if len(res.Unbound) != 0 {
		t.Errorf("unbound declared instances: %v", res.Unbound)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphaned markers: %v", res.Orphans)
	}

	// The identity prefixes are the proof that each address bound to the
	// right kind of live resource, not merely to something.
	for addr, prefix := range map[string]string{
		`aws_vpc.main`:              "vpc-",
		`aws_subnet.this["a"]`:      "subnet-",
		`aws_subnet.this["b"]`:      "subnet-",
		`aws_security_group.main`:   "sg-",
		`aws_route_table.main`:      "rtb-",
		`aws_internet_gateway.main`: "igw-",
		`aws_eip.pool[0]`:           "eipalloc-",
		`aws_eip.pool[1]`:           "eipalloc-",
		`aws_eip.pool[2]`:           "eipalloc-",
	} {
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			continue
		}
		if !strings.HasPrefix(b.ImportID, prefix) {
			t.Errorf("%s bound to %q, which is not a %s… identity", addr, b.ImportID, prefix)
		}
	}

	// The two subnets are the escaping claim: their addresses only survive
	// as tag values because [ and ] and " are rewritten, and they bound by
	// escaped-to-escaped comparison.
	for _, key := range []string{"a", "b"} {
		b, _ := res.BindingFor(mustAddr(t, fmt.Sprintf(`aws_subnet.this[%q]`, key)))
		if b.Marker != "aws_subnet.this:"+key {
			t.Errorf("subnet %q carries marker %q, want the escaped form", key, b.Marker)
		}
		if b.Normalized {
			t.Errorf("subnet %q needed normalizing, so the fixture writes a pre-spec marker", key)
		}
	}

	// The count instances: the P0.1 fixture writes tofu-address in the
	// escaped form directly (MARKERS.md's escaping rule; P2.5a switched
	// compute.tf/logs.tf off the raw-bracket form) - aws_eip.pool:0, not
	// aws_eip.pool[0]. Each of the three is distinct, so they bind one-to-one
	// with no normalization needed - no slot markers required for this
	// estate.
	for i := 0; i < 3; i++ {
		addr := fmt.Sprintf("aws_eip.pool[%d]", i)
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			continue
		}
		t.Logf("count instance %s: marker=%q normalized=%v slot=%q id=%s", addr, b.Marker, b.Normalized, b.Slot, b.ImportID)
		wantMarker := fmt.Sprintf("aws_eip.pool:%d", i)
		if b.Marker != wantMarker {
			t.Errorf("%s carries marker %q, want the escaped form %q", addr, b.Marker, wantMarker)
		}
		if b.Normalized {
			t.Errorf("%s bound with normalization, so the fixture is still writing a pre-spec (unescaped) marker", addr)
		}
		if b.Slot != "" {
			t.Logf("NOTE: %s carries a tofu-slot marker (%q) already; P3.1 landed", addr, b.Slot)
		}
	}
	if len(res.ProblemsOfKind(ProblemNeedsSlotMarkers)) != 0 {
		t.Errorf("the count instances could not be told apart:\n%s", res)
	}

	// Server-side filtering per type, and the fallback for the one type
	// whose list schema offers no filter argument.
	for _, s := range res.Scans {
		t.Logf("SCAN %s", s)
		if s.AccountID == "" {
			t.Errorf("%s listed identities with no account ID: the provider resolved no account, so its owner-id filter went out empty (see stateless/e2e/estate/versions.tf)", s.TypeName)
		}
	}
	if s, _ := res.ScanFor("aws_vpc"); s.Filtering != FilterServerSide {
		t.Errorf("aws_vpc was not filtered server-side: %s", s)
	}
	if s, _ := res.ScanFor("aws_eip"); s.Filtering != FilterClientSide {
		t.Logf("NOTE: aws_eip now supports a list filter; the client-side fallback was not needed: %s", s)
	}
	if len(res.ProblemsOfKind(ProblemUnresolvedAccount)) != 0 {
		t.Errorf("the owner-id warning fired against the fixture:\n%s", res)
	}

	// --- Unclaimed resources, for P2.4 ------------------------------------
	wide, diags := Discover(ctx, Request{
		Estate:           estateName,
		Config:           cfg,
		Resolutions:      resolutions,
		Provider:         provider,
		Region:           awsRegion,
		CollectUnclaimed: true,
	})
	assertNoErrors(t, diags)
	if len(wide.Bindings) != len(res.Bindings) {
		t.Errorf("the wide scan bound %d instances, the filtered scan %d; they must agree", len(wide.Bindings), len(res.Bindings))
	}
	t.Logf("unclaimed resources found by the wide scan: %d", len(wide.Unclaimed))
	for _, u := range wide.Unclaimed {
		t.Logf("  UNCLAIMED %s tags=%v", u, u.Tags)
	}
	if len(wide.Unclaimed) == 0 {
		t.Error("floci's default security group carries no estate marker, so at least one unclaimed resource was expected")
	}

	// --- End to end: the projection the roadmap is after ------------------
	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)

	proj, projDiags := projection.BuildFrom(ctx, cfg, res.Resolutions, provs)
	t.Logf("projection: %d materialized, %d omitted", len(proj.Materialized), len(proj.Omitted))
	for _, addr := range proj.Materialized {
		t.Logf("  MATERIALIZED %s", addr)
	}
	for _, o := range proj.Omitted {
		t.Logf("  OMITTED      %s", o)
	}
	assertNoErrors(t, projDiags)

	// P1.3 materialized six instances: the client-named ones. With markers
	// bound, the marker path and every parent-derived chain that bottomed
	// out in it complete as well.
	want := []string{
		`aws_cloudwatch_log_group.app`,
		`aws_cloudwatch_log_group.optional[0]`,
		`aws_dynamodb_table.events`,
		`aws_ecs_cluster.app`,
		`aws_eip.pool[0]`,
		`aws_eip.pool[1]`,
		`aws_eip.pool[2]`,
		`aws_iam_role.app`,
		`aws_iam_role_policy_attachment.app`,
		`aws_internet_gateway.main`,
		`aws_kms_key.main`,
		`aws_lb.main`,
		`aws_lb_listener.app`,
		`aws_lb_target_group.app`,
		`aws_lb_target_group_attachment.app`,
		`aws_route.internet_gateway`,
		`aws_route53_zone.main`,
		`aws_route_table.main`,
		`aws_route_table_association.this["a"]`,
		`aws_route_table_association.this["b"]`,
		`aws_s3_bucket.data`,
		`aws_s3_bucket_policy.data`,
		`aws_security_group.main`,
		`aws_sns_topic.alerts`,
		`aws_ssm_parameter.demo_effect`,
		`aws_ssm_parameter.demo_existence`,
		`aws_subnet.this["a"]`,
		`aws_subnet.this["b"]`,
		`aws_vpc.main`,
	}
	var got []string
	for _, addr := range proj.Materialized {
		got = append(got, addr.String())
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("materialized %d instances:\n%s\nwant %d:\n%s", len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
	t.Logf("MATERIALIZED COUNT: %d (P1.3 managed 6)", len(proj.Materialized))

	// The parent-derived rows are the whole point: a route and two
	// associations whose identities are composed from marker-discovered
	// parents.
	for _, addr := range []string{
		`aws_route.internet_gateway`,
		`aws_route_table_association.this["a"]`,
		`aws_route_table_association.this["b"]`,
	} {
		is := proj.State.ResourceInstance(mustAddr(t, addr))
		if is == nil || is.Current == nil {
			t.Errorf("%s did not materialize, so the parent-derived chain is still broken", addr)
			continue
		}
		if len(is.Current.AttrsJSON) < 2 {
			t.Errorf("%s materialized as an empty object", addr)
		}
	}

	// Nothing was written: discovery reads tags, and the projection is
	// in-memory.
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after discovery and projection (err = %v)", err)
	}
}

// logMarkerAccessPaths lists one of each needs-discovery type and reports
// where its markers were found, which is the per-type knowledge the tag
// reader depends on.
func logMarkerAccessPaths(ctx context.Context, t *testing.T, provider any) {
	t.Helper()

	schemas, diags := listclient.ListSchemas(ctx, provider)
	if diags.HasErrors() {
		t.Fatalf("ListSchemas: %s", diags.Err())
	}
	for _, typeName := range []string{"aws_vpc", "aws_subnet", "aws_security_group", "aws_route_table", "aws_internet_gateway", "aws_eip"} {
		ts, ok := schemas.Get(typeName)
		if !ok {
			t.Errorf("%s is not listable", typeName)
			continue
		}
		filterOK, why := supportsTagFilter(ts)
		cfg, cfgDiags := ts.BuildConfig(map[string]cty.Value{"region": cty.StringVal(awsRegion)})
		if cfgDiags.HasErrors() {
			t.Errorf("%s BuildConfig: %s", typeName, cfgDiags.Err())
			continue
		}
		results, listDiags := listclient.List(ctx, provider, typeName, cfg, true)
		if listDiags.HasErrors() {
			t.Errorf("%s List: %s", typeName, listDiags.Err())
			continue
		}
		var reported bool
		for _, r := range results {
			tags, taggable := markers.TagsOf(r.Resource)
			if !taggable || tags[TagEstate] != estateName {
				continue
			}
			hasTags := r.Resource.Type().HasAttribute("tags")
			hasTagsAll := r.Resource.Type().HasAttribute("tags_all")
			t.Logf("MARKER PATH %-22s server-filter=%v tags=%v tags_all=%v estate=%q address=%q",
				typeName, filterOK, hasTags, hasTagsAll, tags[TagEstate], tags[TagAddress])
			if !hasTags {
				t.Errorf("%s carries no top-level tags attribute", typeName)
			}
			reported = true
			break
		}
		if !reported {
			t.Errorf("no %s carrying this estate's marker came back from an unfiltered list (filter support: %v %s)", typeName, filterOK, why)
		}
	}
}

// ---------------------------------------------------------------------------
// floci and the estate
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// The real provider plugin
// ---------------------------------------------------------------------------

// launchAWSProvider starts the plugin terraform init downloaded and
// configures it the way the estate's provider block does. It is
// internal/command's providerFactory with the providercache lookup replaced
// by a directory walk, the same as P1.3's integration test.
func launchAWSProvider(t *testing.T, dir string) providers.Interface {
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

	schema, diags := mgr.GetProviderSchema(context.Background(), awsAddr)
	if diags.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", diags.Err())
	}

	// The estate's own provider block, minus skip_requesting_account_id:
	// the provider resolves the account through STS so that the owner-id
	// filter it appends to every filtered EC2 list is not empty. See
	// stateless/e2e/estate/versions.tf.
	const src = `
skip_credentials_validation = true
skip_metadata_api_check     = true
s3_use_path_style           = true
`
	body, hclDiags := hclsyntax.ParseConfig([]byte(src), "provider.hcl", hcl.Pos{Line: 1, Column: 1})
	if hclDiags.HasErrors() {
		t.Fatalf("parsing the provider configuration: %s", hclDiags.Error())
	}
	cfgVal, hclDiags := hcldec.Decode(body.Body, schema.Provider.Block.DecoderSpec(), nil)
	if hclDiags.HasErrors() {
		t.Fatalf("decoding the provider configuration: %s", hclDiags.Error())
	}

	provider, diags := mgr.NewConfiguredProvider(context.Background(), awsAddr, cfgVal)
	if diags.HasErrors() {
		t.Fatalf("configuring the AWS provider: %s", diags.Err())
	}
	return provider
}

func findProviderBinary(t *testing.T, dir string) string {
	t.Helper()

	var found []string
	root := filepath.Join(dir, ".terraform", "providers")
	// Glob, not WalkDir: with TF_PLUGIN_CACHE_DIR set, init links the
	// hostname/namespace/type/version/platform package directory into the
	// cache as a symlink, which a walk does not descend into but a glob's
	// directory reads follow. The five stars are those five levels.
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "*", "*", "*", "terraform-provider-aws*"))
	if err != nil {
		t.Fatalf("looking for the provider plugin under %s: %v", root, err)
	}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("looking for the provider plugin under %s: %v", root, err)
		}
		if info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		t.Fatalf("terraform init left no AWS provider plugin under %s", root)
	}
	sort.Strings(found)
	return found[0]
}

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
