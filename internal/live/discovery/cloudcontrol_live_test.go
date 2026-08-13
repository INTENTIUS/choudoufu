// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This is issue #47's e2e case: a marker-path type with no native provider
// list resource, discovered through Cloud Control instead. It shares
// TestDiscoverAgainstFloci's gate and machinery (real terraform, real AWS
// provider plugin, floci) rather than the fake-server unit tests in
// cloudcontrol_test.go, because the point of an e2e tier is proving the
// wire format and floci's actual behavior, not this package's own logic.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestDiscoverCloudControlFallbackAgainstFloci -v
//
// See testdata/cloudcontrol-e2e/main.tf for why the fixture resource is
// aws_glue_registry rather than the issue's own documented example,
// aws_efs_file_system (floci does not serve EFS at all), and for the
// manual Cloud Control probing that this test's skip path below is based
// on.
func TestDiscoverCloudControlFallbackAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/cloudcontrol")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-cc47")
	endpoint := "http://localhost:" + flociPort

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := copyFixture(t, filepath.Join(flocitest.RepoRoot(t), "internal", "live", "discovery", "testdata", "cloudcontrol-e2e"))
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

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
	// aws_glue_registry has no row in identity.DefaultTable (it is not part
	// of the v0 stateless subset otherwise), so the resolution is built by
	// hand rather than through identity.Resolve, exactly as
	// cloudcontrol_test.go's fake-server tests do: the point of this run is
	// what Discover does once something is already classified as
	// needs-discovery, not the classification itself.
	resolutions := []identity.Resolution{{
		Addr:  mustAddr(t, "aws_glue_registry.demo"),
		Class: identity.ClassNeedsDiscovery,
	}}

	roster, err := registry.Load(
		filepath.Join(flocitest.RepoRoot(t), "live", "mapping.json"),
		filepath.Join(flocitest.RepoRoot(t), "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}
	if _, ok := roster.EnumerationSource("aws_glue_registry"); !ok {
		t.Fatal("aws_glue_registry is not (mapped and listable) in the committed live/mapping.json + live/registry.json; the fixture's premise no longer holds")
	}

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: endpoint, Region: awsRegion})

	res, diags := Discover(ctx, Request{
		Estate:       "cc-e2e",
		Config:       cfg,
		Resolutions:  resolutions,
		Provider:     provider,
		Region:       awsRegion,
		CloudControl: cc,
		Roster:       roster,
	})
	t.Logf("discovery result:\n%s", res)
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor("aws_glue_registry")
	if !ok {
		t.Fatal("no scan was recorded for aws_glue_registry at all")
	}
	if scan.Source != SourceCloudControl {
		t.Fatalf("aws_glue_registry scan source = %q, want %q - the native provider apparently gained a list resource for it", scan.Source, SourceCloudControl)
	}

	if _, ok := res.BindingFor(mustAddr(t, "aws_glue_registry.demo")); !ok {
		// flocitest.CloudControlListCapabilityGate skips with a loud,
		// digest-cited reason when live/floci-capabilities.json already
		// explains this exact gap (it does, as of the pinned digest - see
		// testdata/cloudcontrol-e2e/main.tf for the full probe notes that
		// entry is built from). If the manifest has no matching entry, this
		// falls through to a real failure instead of a silent skip: an
		// unexplained miss here means either floci's Cloud Control coverage
		// regressed, or a new gap needs investigating and recording, not
		// waving through by hand again.
		flocitest.CloudControlListCapabilityGate(t, "aws_glue_registry")
		t.Fatal("aws_glue_registry.demo was not discovered through Cloud Control against floci, and " +
			"live/floci-capabilities.json has no entry explaining why for this floci image - investigate and " +
			"record the finding there (tools/floci-capability-gen's doc comment) rather than skip unexplained")
	}

	// Reached only once floci actually reflects the resource: verify the
	// binding is real, not a fluke.
	b, _ := res.BindingFor(mustAddr(t, "aws_glue_registry.demo"))
	if !strings.Contains(b.ImportID, "cc-e2e-demo") {
		t.Errorf("ImportID = %q, want it to name the registry cc-e2e-demo", b.ImportID)
	}
	if strings.Contains(b.ImportID, "|") {
		t.Errorf("ImportID = %q carries a raw Cloud Control \"|\" separator, which the composite-identifier rule must never emit", b.ImportID)
	}
	if len(res.Unbound) != 0 {
		t.Errorf("unbound declared instances: %v", res.Unbound)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphaned markers: %v", res.Orphans)
	}

	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after discovery (err = %v)", err)
	}
}

// copyFixture is [flocitest.CopyEstate] generalized to an arbitrary fixture
// directory: a scratch copy of its .tf files and .terraform.lock.hcl, so
// terraform's own artifacts never touch the checkout and a cache-trusting
// init finds the lock file it expects.
func copyFixture(t *testing.T, src string) string {
	t.Helper()

	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading the fixture %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".tf") && e.Name() != ".terraform.lock.hcl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name())) //nolint:gosec // a fixed path in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", e.Name(), err)
		}
	}
	return dst
}
