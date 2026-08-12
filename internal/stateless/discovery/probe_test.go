// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"

	goPlugin "github.com/hashicorp/go-plugin"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/logging"
	tfplugin "github.com/opentofu/opentofu/internal/plugin"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
	"github.com/opentofu/opentofu/internal/stateless/listclient"
)

// TestProbeOwnerIDFilter is the evidence behind the roadmap's owner-id
// warning and behind the estate fixture no longer setting
// skip_requesting_account_id.
//
// It puts a recording proxy between the provider and floci and prints the
// DescribeVpcs request the provider actually sends when the list
// configuration carries a tag filter, once with the flag and once without.
// The difference is one filter value: empty, or the account STS answered
// with. Real EC2 matches nothing against the empty one.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/discovery/ -run TestProbeOwnerIDFilter -v
func TestProbeOwnerIDFilter(t *testing.T) {
	flocitest.Gate(t, "owner-id probe")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	flociPort := flocitest.StartFloci(t, "cdf-p23")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := flocitest.CopyEstate(t)
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	exe := findProviderBinary(t, dir)

	rec := &requestRecorder{}
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		rec.add(string(body))
		rp.ServeHTTP(w, r)
	}))
	defer srv.Close()

	for _, skip := range []bool{true, false} {
		t.Run(fmt.Sprintf("skip_requesting_account_id=%v", skip), func(t *testing.T) {
			rec.reset()
			// The plugin subprocess inherits this, so it talks to the
			// recorder instead of to floci directly.
			t.Setenv("AWS_ENDPOINT_URL", srv.URL)

			p := probeProvider(t, exe, skip)
			ctx := context.Background()

			schemas, diags := listclient.ListSchemas(ctx, p)
			if diags.HasErrors() {
				t.Fatalf("ListSchemas: %s", diags.Err())
			}
			vpc, ok := schemas.Get("aws_vpc")
			if !ok {
				t.Fatal("aws_vpc is not listable")
			}
			config, diags := vpc.BuildConfig(map[string]cty.Value{
				"region": cty.StringVal(awsRegion),
				"filter": tagFilter(TagEstate, estateName),
			})
			if diags.HasErrors() {
				t.Fatalf("BuildConfig: %s", diags.Err())
			}
			results, diags := listclient.List(ctx, p, "aws_vpc", config, true)
			if diags.HasErrors() {
				t.Fatalf("List: %s", diags.Err())
			}

			var accountIDs []string
			for _, r := range results {
				if acct, ok := r.IdentityAttr("account_id"); ok {
					accountIDs = append(accountIDs, fmt.Sprintf("%q", acct))
				}
			}
			t.Logf("filtered list returned %d VPCs, identity account_id=%s", len(results), strings.Join(accountIDs, ","))

			var describe string
			for _, b := range rec.bodies() {
				if strings.Contains(b, "Action=DescribeVpcs") {
					describe = decodeForm(b)
				}
				if strings.Contains(b, "Action=GetCallerIdentity") {
					t.Log("REQUEST: " + decodeForm(b))
				}
			}
			if describe == "" {
				t.Fatal("no DescribeVpcs request was recorded")
			}
			t.Log("REQUEST: " + describe)

			// The claim: the provider appends an owner-id filter it builds
			// itself, and its value is empty exactly when the account was
			// never resolved.
			if !strings.Contains(describe, "owner-id") {
				t.Fatalf("the provider sent no owner-id filter at all; the fixture's reason for resolving an account has changed:\n%s", describe)
			}
			empty := strings.Contains(describe, "Filter.3.Value.1= ") || strings.HasSuffix(describe, "Filter.3.Value.1=")
			switch {
			case skip && !empty:
				t.Errorf("with skip_requesting_account_id the owner-id filter was expected to go out empty:\n%s", describe)
			case !skip && empty:
				t.Errorf("without skip_requesting_account_id the owner-id filter still went out empty, so STS did not answer:\n%s", describe)
			case !skip && !strings.Contains(describe, "Filter.3.Value.1=000000000000"):
				t.Errorf("the owner-id filter does not carry floci's account id:\n%s", describe)
			}
		})
	}
}

// requestRecorder keeps every request body that passed through the proxy.
type requestRecorder struct {
	mu   sync.Mutex
	rows []string
}

func (r *requestRecorder) add(b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, b)
}

func (r *requestRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = nil
}

func (r *requestRecorder) bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.rows...)
}

// decodeForm renders an EC2 query-protocol body as sorted key=value pairs.
func decodeForm(b string) string {
	vals, err := url.ParseQuery(b)
	if err != nil {
		return b
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+strings.Join(vals[k], ","))
	}
	return strings.Join(out, " ")
}

// probeProvider launches the provider plugin directly, so that the two
// provider configurations under test differ in exactly one argument.
func probeProvider(t *testing.T, exe string, skipAccountID bool) *tfplugin.GRPCProvider {
	t.Helper()

	client := goPlugin.NewClient(&goPlugin.ClientConfig{
		HandshakeConfig:  tfplugin.Handshake,
		AllowedProtocols: []goPlugin.Protocol{goPlugin.ProtocolGRPC},
		VersionedPlugins: tfplugin.VersionedPlugins,
		Managed:          true,
		Cmd:              exec.Command(exe), //nolint:gosec // the path comes from terraform init in a temp dir
		Logger:           logging.NewProviderLogger(""),
	})
	t.Cleanup(client.Kill)

	rpcClient, err := client.Client()
	if err != nil {
		t.Fatalf("starting the provider: %v", err)
	}
	raw, err := rpcClient.Dispense(tfplugin.ProviderPluginName)
	if err != nil {
		t.Fatalf("dispensing the provider: %v", err)
	}
	p, ok := raw.(*tfplugin.GRPCProvider)
	if !ok {
		t.Fatalf("expected a protocol 5 provider handle, got %T", raw)
	}
	p.PluginClient = client
	p.SchemaCache = providers.NewSchemaCache()

	schema := p.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("GetProviderSchema: %s", schema.Diagnostics.Err())
	}
	block := listclient.TypeSchema{TypeName: "provider", Config: schema.Provider.Block}
	args := map[string]cty.Value{
		"skip_credentials_validation": cty.True,
		"skip_metadata_api_check":     cty.True,
		"s3_use_path_style":           cty.True,
		"region":                      cty.StringVal(awsRegion),
	}
	if skipAccountID {
		args["skip_requesting_account_id"] = cty.True
	}
	config, diags := block.BuildConfig(args)
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
	return p
}
