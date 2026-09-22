// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestTagOnCreateHostedZone is GitHub issue #1084's live pin, over the one
// type on the registry's tag-on-create=false list that the reference estate
// uses: aws_route53_zone. AWS::Route53::HostedZone reads tag_on_create
// false in live/registry.json because CreateHostedZone takes no Tags
// parameter; hashicorp/aws creates the zone and then calls
// ChangeTagsForResource itself when tags are set, so before #1084 the
// ownership marker reached the zone only through a call this fork did not
// make and could not report on.
//
// The maintainer's ruling (2026-09-21): create, then tag in the same apply.
// The node writer withholds the markers from the create call, the live path
// writes them immediately after through the Resource Groups Tagging API's
// TagResources (the same store the discovery sweep reads, bindtags.go), and
// a failed write is an apply error naming the unmarked object by identity
// with the command that marks it.
//
// Two phases, each against its own estate:
//
//  1. Honoured: apply a one-zone estate through a recording proxy. The
//     proxy must see one TagResources call from this fork, the create
//     request must carry no marker (no ChangeTagsForResource body naming
//     tofu-estate; the cache written at run end holds the zone's tags as
//     the provider returned them, marker-free), and the zone must carry
//     the markers by the end of the apply, read back with no tofu in the
//     loop. The next plan must bind the zone by its marker (0 to add).
//  2. Refused: the proxy answers every hosted-zone tag write - the
//     provider's own ChangeTagsForResource and this fork's TagResources -
//     with 403. The apply must fail with an error naming the zone's ARN
//     and printing the aws command that marks it; running that command by
//     hand and planning again must bind the zone (0 to add).
//
// What the pinned emulator does with the write, measured by hand before
// this test existed (aws CLI only, no tofu): route53
// change-tags-for-resource is honoured and read back by route53
// list-tags-for-resource; resourcegroupstaggingapi tag-resources is
// accepted (empty FailedResourcesMap) and read back by
// resourcegroupstaggingapi get-resources - the estate-wide sweep's own
// read - but NOT by route53 list-tags-for-resource, which is what the
// provider's refresh reads; cloudcontrol update-resource on
// AWS::Route53::HostedZone answers UnsupportedOperation. So on floci the
// two tag views are separate stores, where on real AWS the Tagging API
// writes the service's own tags. This test reads the marker back through
// the store discovery reads and logs the route53 view beside it.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestTagOnCreateHostedZone -v
func TestTagOnCreateHostedZone(t *testing.T) {
	flocitest.Gate(t, "tag on create (#1084)")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	port := flocitest.StartFloci(t, "cdf-1084-tagoncreate")
	floci := flocitest.Endpoint(port)
	proxy := newTagWriteProxy(t, floci)

	t.Setenv("AWS_ENDPOINT_URL", proxy.endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	// --- Phase 1: the write is honoured ---------------------------------

	const (
		estateA = "tag-on-create-1084-a"
		zoneA   = "cdf-1084-a.example."
	)
	dirA := t.TempDir()
	tocWriteFixture(t, dirA, estateA, zoneA)
	tofu(t, tofuBin, dirA, "init")

	proxy.reset()
	applied := tofu(t, tofuBin, dirA, "apply", "-auto-approve")
	added, changed, destroyed, ok := applySummary(applied)
	if !ok || added != 1 || changed != 0 || destroyed != 0 {
		t.Fatalf("phase 1: want 1 added / 0 changed / 0 destroyed, got %d/%d/%d (ok=%v)", added, changed, destroyed, ok)
	}

	zoneIDA := tocZoneID(t, floci, zoneA)
	arnA := "arn:aws:route53:::hostedzone/" + zoneIDA
	t.Logf("phase 1: zone %s is %s", zoneA, arnA)

	// This fork's own tag write happened, once, in the same apply.
	if n := proxy.count("ResourceGroupsTaggingAPI_20170126.TagResources"); n != 1 {
		t.Errorf("phase 1: want exactly 1 TagResources call from this run, saw %d (actions: %v)", n, proxy.actions())
	}
	// The create call carried no marker: no provider follow-up
	// ChangeTagsForResource named one.
	for i, body := range proxy.hostedZoneTagWrites() {
		if strings.Contains(body, "tofu-estate") || strings.Contains(body, "tofu-address") {
			t.Errorf("phase 1: ChangeTagsForResource request %d carried an ownership marker; the marker went through the provider's own follow-up call, not this fork's:\n%s", i, body)
		}
	}
	// The applied object, as the provider returned it and as the cache
	// stored it, carries no marker either: this run never sent one to the
	// provider.
	cacheTags := tocCacheTags(t, dirA)
	for _, k := range []string{"tofu-estate", "tofu-address"} {
		if _, has := cacheTags[k]; has {
			t.Errorf("phase 1: the cache's zone tags carry %s; the marker was sent through the provider's create (cache tags: %v)", k, cacheTags)
		}
	}

	// The marker is on the object by the end of the apply, read with no
	// tofu in the loop, through the store the next plan's sweep reads.
	swept := tocSweptTags(t, floci, arnA)
	assertTags(t, swept, "aws_route53_zone.this", map[string]string{
		"tofu-estate":  estateA,
		"tofu-address": "aws_route53_zone.this",
	})
	t.Logf("phase 1: route53 list-tags-for-resource view (the provider's read; separate store on floci): %v",
		tocRoute53Tags(t, floci, zoneIDA))

	// The next plan binds the zone by its marker: nothing to add.
	plan := tofu(t, tofuBin, dirA, "plan")
	if add, change, destroy, ok := flocitest.PlanSummary(plan); ok {
		if add != 0 || destroy != 0 {
			t.Errorf("phase 1: the next plan proposes %d to add / %d to destroy; the zone was not bound by its marker", add, destroy)
		}
		t.Logf("phase 1: next plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	} else if !strings.Contains(plan, "No changes.") {
		t.Errorf("phase 1: the next plan carries neither a summary line nor \"No changes.\"")
	}

	// --- Phase 2: the write is refused ----------------------------------

	const (
		estateB = "tag-on-create-1084-b"
		zoneB   = "cdf-1084-b.example."
	)
	dirB := t.TempDir()
	tocWriteFixture(t, dirB, estateB, zoneB)
	tofu(t, tofuBin, dirB, "init")

	proxy.reset()
	proxy.setRefuse(true)
	refused, err := tocRun(t, tofuBin, dirB, "apply", "-auto-approve")
	proxy.setRefuse(false)
	if err == nil {
		t.Fatalf("phase 2: the apply succeeded with every tag write refused:\n%s", refused)
	}

	zoneIDB := tocZoneID(t, floci, zoneB)
	arnB := "arn:aws:route53:::hostedzone/" + zoneIDB
	t.Logf("phase 2: zone %s is %s", zoneB, arnB)

	if strings.Contains(refused, "Creation complete") {
		t.Errorf("phase 2: the apply reported the zone complete before its marker failed")
	}
	if !strings.Contains(refused, arnB) {
		t.Errorf("phase 2: the apply error does not name the unmarked zone by its ARN %s", arnB)
	}
	command := tocMarkCommand(refused)
	if command == "" {
		t.Fatalf("phase 2: the apply error prints no `aws resourcegroupstaggingapi tag-resources` command:\n%s", refused)
	}
	if !strings.Contains(command, arnB) || !strings.Contains(command, "tofu-estate="+estateB) || !strings.Contains(command, "tofu-address=aws_route53_zone.this") {
		t.Errorf("phase 2: the printed command does not carry the ARN and both markers: %s", command)
	}
	if n := proxy.count("ResourceGroupsTaggingAPI_20170126.TagResources"); n != 1 {
		t.Errorf("phase 2: want exactly 1 refused TagResources attempt, saw %d (actions: %v)", n, proxy.actions())
	}

	// The operator runs the printed command, then plans again.
	tocRunAWSCommand(t, floci, command)
	swept = tocSweptTags(t, floci, arnB)
	assertTags(t, swept, "aws_route53_zone.this", map[string]string{
		"tofu-estate":  estateB,
		"tofu-address": "aws_route53_zone.this",
	})
	plan = tofu(t, tofuBin, dirB, "plan")
	if add, change, destroy, ok := flocitest.PlanSummary(plan); ok {
		if add != 0 || destroy != 0 {
			t.Errorf("phase 2: after marking by hand the plan proposes %d to add / %d to destroy; the zone was not bound by its marker", add, destroy)
		}
		t.Logf("phase 2: plan after the hand-run command: %d to add, %d to change, %d to destroy", add, change, destroy)
	} else if !strings.Contains(plan, "No changes.") {
		t.Errorf("phase 2: the plan after marking carries neither a summary line nor \"No changes.\"")
	}
}

// tocWriteFixture writes the one-zone estate.
func tocWriteFixture(t *testing.T, dir, estate, zone string) {
	t.Helper()
	content := fmt.Sprintf(`terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_requesting_account_id  = true
}

resource "aws_route53_zone" "this" {
  name = %q
}
`, estate, zone)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}

// tocRun runs the binary and returns its combined output and error, for
// the apply this test expects to fail.
func tocRun(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{args[0], "-no-color", "-input=false"}, args[1:]...)
	cmd := exec.Command(bin, full...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("choudoufu %s (err=%v)\n%s", strings.Join(full, " "), err, out)
	return string(out), err
}

// tocAWS runs one AWS CLI call straight against floci, bypassing the
// proxy, and returns its trimmed stdout.
func tocAWS(t *testing.T, endpoint string, args ...string) string {
	t.Helper()
	full := append([]string{"--endpoint-url", endpoint}, args...)
	cmd := exec.Command("aws", full...) //nolint:gosec // fixed binary, test-only
	cmd.Env = append(os.Environ(), "AWS_ENDPOINT_URL="+endpoint)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("aws %s failed: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// tocZoneID finds the hosted zone named name, as the bare ID (no
// /hostedzone/ prefix).
func tocZoneID(t *testing.T, endpoint, name string) string {
	t.Helper()
	out := tocAWS(t, endpoint, "route53", "list-hosted-zones",
		"--query", fmt.Sprintf("HostedZones[?Name=='%s'].Id", name), "--output", "json")
	var ids []string
	if err := json.Unmarshal([]byte(out), &ids); err != nil {
		t.Fatalf("decoding list-hosted-zones %q: %v", out, err)
	}
	if len(ids) != 1 {
		t.Fatalf("want exactly one hosted zone named %s, found %v", name, ids)
	}
	return strings.TrimPrefix(ids[0], "/hostedzone/")
}

// tocSweptTags reads the tags the Resource Groups Tagging API holds for
// arn - the read the discovery sweep makes.
func tocSweptTags(t *testing.T, endpoint, arn string) map[string]string {
	t.Helper()
	out := tocAWS(t, endpoint, "resourcegroupstaggingapi", "get-resources",
		"--resource-type-filters", "route53:hostedzone",
		"--query", fmt.Sprintf("ResourceTagMappingList[?ResourceARN=='%s'].Tags[]", arn), "--output", "json")
	return decodeTagList(t, out)
}

// tocRoute53Tags reads the tags Route 53 itself holds for the zone.
func tocRoute53Tags(t *testing.T, endpoint, zoneID string) map[string]string {
	t.Helper()
	out := tocAWS(t, endpoint, "route53", "list-tags-for-resource",
		"--resource-type", "hostedzone", "--resource-id", zoneID,
		"--query", "ResourceTagSet.Tags", "--output", "json")
	return decodeTagList(t, out)
}

// tocCacheTags reads the zone's tags out of the state cache the run wrote
// at its end.
func tocCacheTags(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".terraform", "choudoufu-cache.tfstate"))
	if err != nil {
		t.Fatalf("reading the state cache: %v", err)
	}
	var state struct {
		Resources []struct {
			Type      string `json:"type"`
			Instances []struct {
				Attributes struct {
					Tags map[string]string `json:"tags"`
				} `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decoding the state cache: %v", err)
	}
	for _, r := range state.Resources {
		if r.Type == "aws_route53_zone" && len(r.Instances) == 1 {
			return r.Instances[0].Attributes.Tags
		}
	}
	t.Fatalf("the state cache holds no aws_route53_zone instance")
	return nil
}

var tocMarkCommandLine = regexp.MustCompile(`(?m)^\s*(aws resourcegroupstaggingapi tag-resources .*)$`)

// tocMarkCommand extracts the marking command the apply error prints, or
// "" when there is none.
func tocMarkCommand(output string) string {
	m := tocMarkCommandLine.FindStringSubmatch(output)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// tocRunAWSCommand runs the printed command exactly as an operator would,
// with the endpoint override pointed at floci.
func tocRunAWSCommand(t *testing.T, endpoint, command string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command) //nolint:gosec // the command under test, against a local emulator
	cmd.Env = append(os.Environ(), "AWS_ENDPOINT_URL="+endpoint)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the printed command %q: %v\n%s", command, err, out)
	}
	t.Logf("ran the printed command: %s\n%s", command, out)
}

// tagWriteProxy is a reverse proxy in front of floci that records every
// request's action and the body of every hosted-zone tag write, and, when
// refusing, answers every hosted-zone tag write - Route 53's own
// ChangeTagsForResource and the Tagging API's TagResources - with 403.
type tagWriteProxy struct {
	endpoint string
	mu       sync.Mutex
	refuse   bool
	counts   map[string]int
	zoneTags []string
}

func newTagWriteProxy(t *testing.T, target string) *tagWriteProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing the proxy target %q: %v", target, err)
	}
	p := &tagWriteProxy{counts: map[string]int{}}
	rp := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("X-Amz-Target")
		if action == "" {
			action = r.Method + " " + r.URL.Path
		}
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		isZoneTagWrite := r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tags/hostedzone/")
		isTagResources := action == "ResourceGroupsTaggingAPI_20170126.TagResources"

		p.mu.Lock()
		p.counts[action]++
		if isZoneTagWrite {
			p.zoneTags = append(p.zoneTags, string(body))
		}
		refuse := p.refuse
		p.mu.Unlock()

		if refuse && isZoneTagWrite {
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<?xml version="1.0"?><ErrorResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>refused by TestTagOnCreateHostedZone</Message></Error><RequestId>1084</RequestId></ErrorResponse>`)
			return
		}
		if refuse && isTagResources {
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"__type":"AccessDeniedException","message":"refused by TestTagOnCreateHostedZone"}`)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing the proxy's own URL: %v", err)
	}
	// "localhost" rather than 127.0.0.1 for the same reason
	// flocitest.NewCountingProxy gives: a virtual-hosted call prefixes a
	// label onto the host, which an IP literal cannot take.
	p.endpoint = "http://localhost:" + srvURL.Port()
	return p
}

func (p *tagWriteProxy) setRefuse(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refuse = on
}

func (p *tagWriteProxy) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts = map[string]int{}
	p.zoneTags = nil
}

func (p *tagWriteProxy) count(action string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[action]
}

func (p *tagWriteProxy) actions() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.counts))
	for k, v := range p.counts {
		out[k] = v
	}
	return out
}

func (p *tagWriteProxy) hostedZoneTagWrites() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.zoneTags...)
}
