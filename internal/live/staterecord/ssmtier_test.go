// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// newTieredSSMStore is [newTestSSMStore] with two differences: the tier is
// configurable, and every PutParameter request body is captured BEFORE the
// fake server sees it.
//
// The capture is the point. A round trip through this package agrees with
// itself in every tier - Get returns whatever Put stored regardless of what
// Tier was sent, because the fake has no tier concept and neither does real
// Parameter Store's read path. The only external witness that a tier was
// actually selected is the request AWS would have received, so that is what
// these tests read. Compare [SSMStore.ParameterName]'s own doc comment for
// the same reasoning about names.
func newTieredSSMStore(t *testing.T, tier SSMTier) (*SSMStore, *[]map[string]any) {
	t.Helper()
	fake := &fakeSSMServer{params: map[string]*fakeSSMParameter{}}
	var puts []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("X-Amz-Target"), "PutParameter") {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading the PutParameter body: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decoding the PutParameter body %q: %v", raw, err)
			}
			puts = append(puts, body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		fake.handle(w, r)
	}))
	t.Cleanup(server.Close)

	client := ssm.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *ssm.Options) {
		o.BaseEndpoint = aws.String(server.URL)
	})
	store, err := NewSSMStore(SSMConfig{Client: client, KeyPrefix: "/choudoufu-test", Tier: tier})
	if err != nil {
		t.Fatalf("NewSSMStore: %v", err)
	}
	return store, &puts
}

// TestSSMTierReachesTheWire is GitHub issue #1146's escape hatch, proven
// where it can be proven: on the request, not on a round trip.
//
// It covers BOTH PutParameter call sites - the create (PutIfAbsent,
// Overwrite false) and the update (PutIfVersion, Overwrite true) - because
// a tier plumbed into one and not the other writes an estate whose records
// change tier the first time they are updated, and a revert from advanced
// back to standard truncates an 8KB value to 4KB.
func TestSSMTierReachesTheWire(t *testing.T) {
	for _, tc := range []struct {
		tier SSMTier
		want string // "" means: no Tier member at all in the request
	}{
		{SSMTierUnset, ""},
		{SSMTierStandard, "Standard"},
		{SSMTierAdvanced, "Advanced"},
		{SSMTierIntelligent, "Intelligent-Tiering"},
	} {
		t.Run(string(tc.tier)+"/unset-is-blank", func(t *testing.T) {
			ctx := context.Background()
			store, puts := newTieredSSMStore(t, tc.tier)

			version, err := store.PutIfAbsent(ctx, "k", []byte("one"))
			if err != nil {
				t.Fatalf("PutIfAbsent: %v", err)
			}
			if _, err := store.PutIfVersion(ctx, "k", []byte("two"), version); err != nil {
				t.Fatalf("PutIfVersion: %v", err)
			}

			if len(*puts) != 2 {
				t.Fatalf("saw %d PutParameter calls, want 2 (one create, one update)", len(*puts))
			}
			for i, body := range *puts {
				got, present := body["Tier"]
				switch {
				case tc.want == "" && present:
					t.Errorf("call %d sent Tier=%v; an unset tier must send none at all, so the account's own default-tier configuration decides", i, got)
				case tc.want == "":
					// Correct: no Tier member.
				case !present:
					t.Errorf("call %d sent no Tier at all, want %q", i, tc.want)
				case got != tc.want:
					t.Errorf("call %d sent Tier=%v, want %q", i, got, tc.want)
				}
			}
		})
	}
}

// TestSSMTierCapacityIsTheQuota pins the numbers against their source:
// aws-sdk-go-v2/service/ssm@v1.62.0's PutParameterInput documentation, and
// Service Quotas L-C3B871CB, which is listed Adjustable: False.
//
// Intelligent-Tiering shares the advanced ceiling because AWS's own listed
// conditions for creating an advanced parameter include "more than 10,000
// parameters already exist in your account in the current Region" - so an
// intelligent-tiered estate crosses 10,000 rather than stopping there.
func TestSSMTierCapacityIsTheQuota(t *testing.T) {
	for _, tc := range []struct {
		tier     SSMTier
		capacity int
		value    int
	}{
		{SSMTierUnset, 10000, 4096},
		{SSMTierStandard, 10000, 4096},
		{SSMTierAdvanced, 100000, 8192},
		{SSMTierIntelligent, 100000, 8192},
		{SSMTier("gold"), 10000, 4096}, // unknown prices as the smallest ceiling
	} {
		if got := SSMTierCapacity(tc.tier); got != tc.capacity {
			t.Errorf("SSMTierCapacity(%q) = %d, want %d", tc.tier, got, tc.capacity)
		}
		if got := SSMTierValueLimit(tc.tier); got != tc.value {
			t.Errorf("SSMTierValueLimit(%q) = %d, want %d", tc.tier, got, tc.value)
		}
	}
	if SSMStandardParameterLimit != 10000 || SSMAdvancedParameterLimit != 100000 {
		t.Errorf("the quota constants moved: standard %d, advanced %d", SSMStandardParameterLimit, SSMAdvancedParameterLimit)
	}
	// GitHub issue #1283's measurement, restated here because the doc
	// comment now states both ceilings and a reader must be able to find
	// which test holds each one.
	if SSMParameterNameLimit != 1011 || SSMParameterHierarchyLimit != 15 {
		t.Errorf("the name-limit constants moved: %d characters, %d levels", SSMParameterNameLimit, SSMParameterHierarchyLimit)
	}
}

// TestNewSSMStoreRefusesAnUnknownTier: a tier this package does not know
// would otherwise reach PutParameter as a literal string AWS rejects, one
// record at a time, mid-apply.
func TestNewSSMStoreRefusesAnUnknownTier(t *testing.T) {
	_, err := NewSSMStore(SSMConfig{Client: &ssm.Client{}, Tier: SSMTier("Advanced")})
	if err == nil {
		t.Fatal("NewSSMStore accepted the tier \"Advanced\"; the fork's spelling is lower-case")
	}
	for _, want := range append([]string{`"Advanced"`}, SSMTierNames()...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s:\n%s", want, err)
		}
	}
}

// TestSSMTierNamesExcludesTheDefault: the default is spelled by omitting
// the argument, so listing "" among the valid values would tell an author
// to write tier = "".
func TestSSMTierNamesExcludesTheDefault(t *testing.T) {
	names := SSMTierNames()
	if slices.Contains(names, "") {
		t.Errorf("SSMTierNames() offers the empty tier: %q", names)
	}
	for _, n := range names {
		if _, ok := ParseSSMTier(n); !ok {
			t.Errorf("SSMTierNames() offers %q, which ParseSSMTier rejects", n)
		}
	}
	if _, ok := ParseSSMTier(""); !ok {
		t.Error("ParseSSMTier(\"\") is not accepted; omitting the argument must stay valid")
	}
	if _, ok := ParseSSMTier("gold"); ok {
		t.Error("ParseSSMTier accepted \"gold\"")
	}
}
