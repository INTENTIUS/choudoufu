// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// fakeCloudControl is a Cloud Control endpoint that reproduces the exact
// emulator behavior this sweep exists to tell apart: a CreateResource that
// really does create, alongside a ListResources that may or may not
// enumerate what was created. Both halves speak the same AWS JSON 1.0 wire
// shape floci does - the ProgressEvent envelope with a RequestToken, the
// IN_PROGRESS-then-SUCCESS two-step - so a test here is exercising the
// classification against the response bodies, not against a mock of it.
type fakeCloudControl struct {
	mu sync.Mutex

	// enumerates names the types whose ListResources actually returns what
	// CreateResource made. A type absent from it lists empty forever, which
	// is AWS::CloudFront::CachePolicy's real behavior on floci.
	enumerates map[string]bool

	// createRefusal maps a type to an AWS error code its CreateResource
	// answers with instead of creating anything.
	createRefusal map[string]string

	// blankProperties names the types whose ListResources hands back the
	// identifier with an empty model - enumerated, but with nothing to
	// match a configured resource against.
	blankProperties map[string]bool

	// listStatus/listBody override the ListResources response wholesale,
	// for the router-refusal and broken-handler cases.
	listStatus int
	listBody   string

	created map[string][]string // type -> identifiers created so far
	tokens  map[string][2]string
	seq     int
}

func newFakeCloudControl() *fakeCloudControl {
	return &fakeCloudControl{
		enumerates:      map[string]bool{},
		createRefusal:   map[string]string{},
		blankProperties: map[string]bool{},
		created:         map[string][]string{},
		tokens:          map[string][2]string{},
	}
}

func (f *fakeCloudControl) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeCloudControl) serve(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	var req struct {
		TypeName     string `json:"TypeName"`
		RequestToken string `json:"RequestToken"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.HasSuffix(target, ".ListResources"):
		if f.listBody != "" {
			w.WriteHeader(f.listStatus)
			_, _ = w.Write([]byte(f.listBody))
			return
		}
		descs := []any{}
		if f.enumerates[req.TypeName] {
			for _, id := range f.created[req.TypeName] {
				props := fmt.Sprintf(`{"Id":%q}`, id)
				if f.blankProperties[req.TypeName] {
					props = "{}"
				}
				descs = append(descs, map[string]string{"Identifier": id, "Properties": props})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": descs})

	case strings.HasSuffix(target, ".CreateResource"):
		if code, refused := f.createRefusal[req.TypeName]; refused {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"__type":  "com.amazonaws.cloudformation#" + code,
				"message": "no create for " + req.TypeName,
			})
			return
		}
		f.seq++
		id := fmt.Sprintf("cloudcontrol-resource-%04d", f.seq)
		token := fmt.Sprintf("token-%04d", f.seq)
		f.created[req.TypeName] = append(f.created[req.TypeName], id)
		f.tokens[token] = [2]string{req.TypeName, id}
		_ = json.NewEncoder(w).Encode(map[string]any{"ProgressEvent": map[string]string{
			"TypeName": req.TypeName, "RequestToken": token, "OperationStatus": "IN_PROGRESS",
		}})

	case strings.HasSuffix(target, ".GetResourceRequestStatus"):
		entry, ok := f.tokens[req.RequestToken]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "RequestTokenNotFoundException", "message": "no such token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ProgressEvent": map[string]string{
			"TypeName": entry[0], "Identifier": entry[1], "RequestToken": req.RequestToken, "OperationStatus": "SUCCESS",
		}})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func classifyAgainst(t *testing.T, f *fakeCloudControl, tfType, cfnType string) typeRow {
	t.Helper()
	server := f.start(t)
	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	row, err := classifyListResources(context.Background(), cc, newSeeder(server.URL), tfType, cfnType)
	if err != nil {
		t.Fatalf("classifyListResources(%s): %v", cfnType, err)
	}
	if row.Mechanism != "cloudcontrol-list" {
		t.Errorf("Mechanism = %q, want cloudcontrol-list", row.Mechanism)
	}
	return row
}

// TestClassifyRoundTripCloses is the only shape that may read implemented:
// the resource CreateResource made comes back out of ListResources.
func TestClassifyRoundTripCloses(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::EC2::VPC"] = true

	row := classifyAgainst(t, f, "aws_vpc", "AWS::EC2::VPC")
	if row.Status != "implemented" {
		t.Errorf("Status = %q, want implemented (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "the round trip closed") {
		t.Errorf("Evidence does not say a round trip was checked: %q", row.Evidence)
	}
	if !strings.Contains(row.Evidence, "CreateResource") {
		t.Errorf("Evidence does not name the create it round-tripped through: %q", row.Evidence)
	}
}

// TestEvidenceDoesNotVaryWithTheGeneratedIdentifier guards the artifact's
// reproducibility. The emulator names every resource it creates with fresh
// randomness, so an evidence string that quoted the identifier back made
// live/floci-capabilities.json differ on ~590 of 610 rows between two runs
// against the same image - measured, before this was fixed - which would
// have left the file's diff carrying no information at all.
func TestEvidenceDoesNotVaryWithTheGeneratedIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enumerates bool
	}{
		{"round trip closes", true},
		{"list stays empty", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var evidence []string
			for run := 0; run < 2; run++ {
				f := newFakeCloudControl()
				f.enumerates["AWS::T::T"] = tc.enumerates
				// A different identifier space per run, the way two floci
				// containers name the same resource differently.
				f.seq = run * 1000
				evidence = append(evidence, classifyAgainst(t, f, "aws_t", "AWS::T::T").Evidence)
			}
			if evidence[0] != evidence[1] {
				t.Errorf("two runs produced different evidence:\n  %q\n  %q", evidence[0], evidence[1])
			}
		})
	}
}

// TestClassifyCreateSucceedsListStaysEmpty is the defect this sweep was
// rewritten for, and floci's real behavior for
// AWS::CloudFront::CachePolicy and AWS::Route53::CidrCollection: the create
// really does create, and ListResources answers an empty list, cleanly,
// forever. The old sweep called that implemented on the strength of the
// call returning.
func TestClassifyCreateSucceedsListStaysEmpty(t *testing.T) {
	f := newFakeCloudControl() // nothing registered in enumerates

	row := classifyAgainst(t, f, "aws_cloudfront_cache_policy", "AWS::CloudFront::CachePolicy")
	if row.Status != "unimplemented" {
		t.Errorf("Status = %q, want unimplemented (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "none of them the identifier the create had just named") {
		t.Errorf("Evidence does not say what was looked for and missed: %q", row.Evidence)
	}
	if strings.Contains(row.Evidence, "succeeded") {
		t.Errorf("Evidence still reads as a bare-call claim: %q", row.Evidence)
	}
}

// TestClassifyEnumeratedWithBlankModelIsPartial covers the third failure
// shape a discovery leg can hit: the list does come back carrying the
// resource, and carries nothing about it. cloudfront's own
// list-public-keys is the native-API instance (every item returned with
// Name unset, readable only one get-public-key at a time); this is its
// Cloud Control form. An identifier with no attributes is not something a
// discovery leg can match a configured resource against, so it is not
// "implemented", and it is not nothing either.
func TestClassifyEnumeratedWithBlankModelIsPartial(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::CloudFront::PublicKey"] = true
	f.blankProperties["AWS::CloudFront::PublicKey"] = true

	row := classifyAgainst(t, f, "aws_cloudfront_public_key", "AWS::CloudFront::PublicKey")
	if row.Status != "partial" {
		t.Errorf("Status = %q, want partial (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "empty Properties model") {
		t.Errorf("Evidence does not say what was missing: %q", row.Evidence)
	}
}

// TestClassifyCreateRefusedIsUnverified: a list that returns cleanly with
// nothing to look for in it settles nothing, and must say so rather than
// pick either of the two answers it cannot distinguish.
func TestClassifyCreateRefusedIsUnverified(t *testing.T) {
	f := newFakeCloudControl()
	f.createRefusal["AWS::Some::Type"] = "ValidationException"

	row := classifyAgainst(t, f, "aws_some_type", "AWS::Some::Type")
	if row.Status != "unverified" {
		t.Errorf("Status = %q, want unverified (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "nothing could be created to prove it answers") {
		t.Errorf("Evidence does not say why nothing was established: %q", row.Evidence)
	}
	if !strings.Contains(row.Evidence, "ValidationException") {
		t.Errorf("Evidence does not carry the refusal it saw: %q", row.Evidence)
	}
}

// TestClassifyUnsupportedOperation covers floci's router refusing a type
// outright - unimplemented, the wire shape
// internal/live/cloudcontrol/client_test.go's own TestErrorCodeMapping
// exercises at the client layer.
func TestClassifyUnsupportedOperation(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusBadRequest
	f.listBody = `{"__type":"com.amazonaws.cloudformation#UnsupportedOperation","message":"ListResources is not supported"}`

	row := classifyAgainst(t, f, "aws_networkmanager_core_network", "AWS::NetworkManager::CoreNetwork")
	if row.Status != "unimplemented" {
		t.Errorf("Status = %q, want unimplemented (%s)", row.Status, row.Evidence)
	}
}

// TestClassifyBrokenHandler covers the HTML-error-page shape the databases
// and stragglers cohort READMEs both document: HTTP round trips, but the
// body carries no __type at all, so the client's own APIError.Code comes
// back empty - a router-recognized but broken handler, not an absent one.
func TestClassifyBrokenHandler(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusInternalServerError
	f.listBody = "<html>not json</html>"

	row := classifyAgainst(t, f, "aws_docdbelastic_cluster", "AWS::DocDBElastic::Cluster")
	if row.Status != "broken" {
		t.Errorf("Status = %q, want broken (%s)", row.Status, row.Evidence)
	}
}

// TestClassifyOrdinaryListErrorIsUnverified: a handler that answers its own
// ordinary error shape is a real handler, but it enumerated nothing, so it
// is no evidence a discovery leg could find anything through it. The old
// sweep recorded this implemented on "reached a real handler".
func TestClassifyOrdinaryListErrorIsUnverified(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusBadRequest
	f.listBody = `{"__type":"com.amazonaws.cloudformation#ValidationException","message":"1 validation error detected"}`

	row := classifyAgainst(t, f, "aws_some_type", "AWS::Some::Type")
	if row.Status != "unverified" {
		t.Errorf("Status = %q, want unverified (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "enumerated nothing") {
		t.Errorf("Evidence does not say the handler enumerated nothing: %q", row.Evidence)
	}
}

// TestNoEvidenceStringClaimsMoreThanItChecked is the guard against the
// defect returning in prose form: every verdict this classifier can reach
// must describe the calls it made, and none of them may fall back on the
// bare "succeeded" wording the old sweep used, which read as a round trip
// to anyone planning a crossing against it.
func TestNoEvidenceStringClaimsMoreThanItChecked(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCloudControl)
	}{
		{"round trip closes", func(f *fakeCloudControl) { f.enumerates["AWS::T::T"] = true }},
		{"list stays empty", func(f *fakeCloudControl) {}},
		{"create refused", func(f *fakeCloudControl) { f.createRefusal["AWS::T::T"] = "ValidationException" }},
		{"router refuses list", func(f *fakeCloudControl) {
			f.listStatus = http.StatusBadRequest
			f.listBody = `{"__type":"UnsupportedOperation","message":"nope"}`
		}},
		{"broken handler", func(f *fakeCloudControl) {
			f.listStatus = http.StatusInternalServerError
			f.listBody = "<html/>"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCloudControl()
			tc.setup(f)
			row := classifyAgainst(t, f, "aws_t", "AWS::T::T")
			if row.Evidence == "" {
				t.Fatal("Evidence is empty")
			}
			if !strings.Contains(row.Evidence, "AWS::T::T") {
				t.Errorf("Evidence does not name the type it called: %q", row.Evidence)
			}
			if !strings.Contains(row.Evidence, "ListResources") {
				t.Errorf("Evidence does not name the call it made: %q", row.Evidence)
			}
			if row.Status == "implemented" && !strings.Contains(row.Evidence, "CreateResource") {
				t.Errorf("an implemented verdict whose evidence does not cite a create is a bare-call claim: %q", row.Evidence)
			}
			if !strings.Contains(row.Source, "round trip") {
				t.Errorf("Source does not say what kind of probe wrote this: %q", row.Source)
			}
		})
	}
}
