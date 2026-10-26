// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// fakeTagIndex is a Resource Groups Tagging API endpoint that answers
// GetResources with mappings and records each request's filters. Every
// other operation is refused, so a Cloud Control or service call the run
// makes reads as an ordinary failed fallback.
type fakeTagIndex struct {
	mappings []map[string]any

	mu       sync.Mutex
	requests []fakeTagIndexRequest
}

type fakeTagIndexRequest struct {
	ResourceTypeFilters []string `json:"ResourceTypeFilters"`
	TagFilters          []struct {
		Key    string   `json:"Key"`
		Values []string `json:"Values"`
	} `json:"TagFilters"`
}

func (f *fakeTagIndex) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".GetResources") {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"__type":"UnknownOperationException","message":"not served by this fake"}`))
		return
	}
	var req fakeTagIndexRequest
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": f.mappings})
}

// TestLivePlanResource_tagIndexBindsATypeWhoseListDropsTags is GitHub issue
// #2260's tag-index gap. The VPC's list call returns it with no tags, so its
// marker can only be read off the tag index. Before #2260 -resource turned
// the index off and the VPC read not-observed (no-binding); now the index is
// read for the cone's types alone and the VPC reads in-sync, with the one
// GetResources call filtered to ec2:vpc and the estate's tag, counted in its
// own field, and the store still never listed and nothing swept.
func TestLivePlanResource_tagIndexBindsATypeWhoseListDropsTags(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
	t.Chdir(td)

	index := &fakeTagIndex{mappings: []map[string]any{{
		"ResourceARN": "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-owned",
		"Tags": []map[string]string{
			{"Key": "tofu-estate", "Value": "live-unit"},
			{"Key": "tofu-address", "Value": "aws_vpc.main"},
		},
	}}}
	srv := httptest.NewServer(index)
	defer srv.Close()
	t.Setenv(cloudControlEnvVar, "on")
	t.Setenv("AWS_ENDPOINT_URL_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	store, sweeper := &fakeRecordStore{}, &fakeSweeper{}
	installResourcePathFakes(t, store, sweeper)

	cloud := newLiveTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-live-unit-data", "live-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-live-unit-data", "bucket": "tofu-live-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "live-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	// Listed with no tags: the list call dropped them, as iam:ListRoles does.
	cloud.list("aws_vpc", "vpc-owned", "the estate's VPC", nil, map[string]string{"cidr_block": "10.42.0.0/16"})

	view, done := testView(t)
	c := &LivePlanCommand{Meta: liveBlockMeta(view, cloud)}
	code := c.Run([]string{"-no-color", "-resource=aws_vpc.main"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}

	_, records, settle := driftLines(t, out.Stdout())
	if len(records) == 0 || records[len(records)-1]["address"] != "aws_vpc.main" {
		t.Fatalf("records = %v, want aws_vpc.main's", records)
	}
	if got := records[len(records)-1]["status"]; got != "in-sync" {
		t.Errorf("aws_vpc.main status %v, want in-sync: the tag index carries its marker", got)
	}
	reads, _ := settle["reads"].(map[string]any)
	if reads["recordListCalls"] != float64(0) || reads["sweepCalls"] != float64(0) {
		t.Errorf("drift-settle reads = %v, want recordListCalls and sweepCalls 0", reads)
	}
	if reads["tagIndexCalls"] != float64(1) {
		t.Errorf("drift-settle tagIndexCalls = %v, want 1", reads["tagIndexCalls"])
	}

	index.mu.Lock()
	defer index.mu.Unlock()
	if len(index.requests) != 1 {
		t.Fatalf("the tag index was asked %d time(s), want once", len(index.requests))
	}
	req := index.requests[0]
	if strings.Join(req.ResourceTypeFilters, ",") != "ec2:vpc" {
		t.Errorf("GetResources ResourceTypeFilters = %q, want [ec2:vpc]: the cone's types alone", req.ResourceTypeFilters)
	}
	if len(req.TagFilters) != 1 || req.TagFilters[0].Key != "tofu-estate" || strings.Join(req.TagFilters[0].Values, ",") != "live-unit" {
		t.Errorf("GetResources TagFilters = %+v, want tofu-estate=live-unit", req.TagFilters)
	}
	if built, swept := sweeper.counts(); built != 0 || swept != 0 {
		t.Errorf("the sweep leg was built %d time(s) and ran %d time(s); want neither", built, swept)
	}
}

// TestLivePlanResource_streamsRecordsAsThePlanWalks is GitHub issue #2260's
// streaming gap. The cone of aws_s3_bucket.data holds the VPC it depends
// on, which the plan walk plans first. The fake provider's plan of the
// bucket blocks until the VPC's drift-record line has been written, so the
// run finishes only if records are written as the walk plans them; when
// records were written after the plan, this waited out its timeout.
func TestLivePlanResource_streamsRecordsAsThePlanWalks(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-resource-stream"), td)
	t.Chdir(td)

	store, sweeper := &fakeRecordStore{}, &fakeSweeper{}
	installResourcePathFakes(t, store, sweeper)

	vpcWritten := make(chan struct{})
	var once sync.Once
	prevHook := liveDriftRecordWritten
	liveDriftRecordWritten = func(address string) {
		if address == "aws_vpc.main" {
			once.Do(func() { close(vpcWritten) })
		}
	}
	t.Cleanup(func() { liveDriftRecordWritten = prevHook })

	cloud := liveBlockCloud()
	inst := cloud.provider().(*liveTestProvider)
	plan := inst.PlanResourceChangeFn
	var waited, timedOut bool
	var waitMu sync.Mutex
	inst.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if req.TypeName == "aws_s3_bucket" && !req.PriorState.IsNull() {
			select {
			case <-vpcWritten:
				waitMu.Lock()
				waited = true
				waitMu.Unlock()
			case <-time.After(10 * time.Second):
				waitMu.Lock()
				timedOut = true
				waitMu.Unlock()
			}
		}
		return plan(req)
	}

	view, done := testView(t)
	c := &LivePlanCommand{Meta: Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{
			Providers: map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("aws"): providers.FactoryFixed(inst),
			},
		},
	}}
	code := c.Run([]string{"-no-color", "-resource=aws_s3_bucket.data"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	waitMu.Lock()
	defer waitMu.Unlock()
	if timedOut || !waited {
		t.Fatalf("the bucket's plan waited for the VPC's drift-record line and it never came (waited %v, timed out %v): records are not written as the plan walks", waited, timedOut)
	}

	_, records, _ := driftLines(t, out.Stdout())
	last := map[string]string{}
	for _, r := range records {
		last[r["address"].(string)] = r["status"].(string)
	}
	for _, addr := range []string{"aws_vpc.main", "aws_s3_bucket.data"} {
		if last[addr] != "in-sync" {
			t.Errorf("%s's last record reads %q, want in-sync; records %v", addr, last[addr], records)
		}
	}
}

// TestLiveResourceConeLegs_narrowsEachFamilyLeg: the Azure, GCP and
// Kubernetes legs -resource builds are the estate sweep's legs with Cone
// set to the cone's types, and an AWS provider gets none.
func TestLiveResourceConeLegs_narrowsEachFamilyLeg(t *testing.T) {
	cone := map[string]bool{"example_type": true}
	for _, tc := range []struct {
		sub substrate.Substrate
		leg discovery.Sweeper
	}{
		{substrate.Kubernetes, discovery.KubernetesSweep{}},
		{substrate.Google, discovery.GCPAssetSweep{}},
		{substrate.Azure, discovery.ResourceGraphSweep{}},
	} {
		t.Run(tc.sub.Name(), func(t *testing.T) {
			prev := sweepLegBuilders[tc.sub.Sweep()]
			leg := tc.leg
			sweepLegBuilders[tc.sub.Sweep()] = func(context.Context, *projectionProviders, substrate.Substrate, addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
				return leg, false, nil
			}
			t.Cleanup(func() { sweepLegBuilders[tc.sub.Sweep()] = prev })

			legs, diags := liveResourceConeLegs(context.Background(), &projectionProviders{}, tc.sub, addrs.AbsProviderConfig{}, cone)
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			if len(legs) != 1 {
				t.Fatalf("legs = %v, want one", legs)
			}
			var got map[string]bool
			switch l := legs[0].(type) {
			case discovery.KubernetesSweep:
				got = l.Cone
			case discovery.GCPAssetSweep:
				got = l.Cone
			case discovery.ResourceGraphSweep:
				got = l.Cone
			default:
				t.Fatalf("leg is %T", legs[0])
			}
			if !got["example_type"] || len(got) != 1 {
				t.Errorf("leg Cone = %v, want the cone's types", got)
			}
		})
	}
	if legs, _ := liveResourceConeLegs(context.Background(), &projectionProviders{}, substrate.AWS, addrs.AbsProviderConfig{}, cone); len(legs) != 0 {
		t.Errorf("the AWS family got cone legs %v; its instances bind through the config-driven scan and the tag index", legs)
	}
}
