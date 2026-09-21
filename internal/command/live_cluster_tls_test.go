// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1448, section C: what an operator sees of tls_verification,
// in `live-cluster` and on an apply. The finding is the one the contract
// itself produces for a block that sets `insecure = true`, read here rather
// than retyped, so these tests cannot pass on text the product never prints.

// insecureClusterFindings is a cluster that passes everything, reached
// through a block that sets `insecure = true`.
func insecureClusterFindings(t *testing.T) []staterecord.Finding {
	t.Helper()
	// The fake's tracker has no schema for a SelfSubjectAccessReview, so the
	// review is answered here. What it answers does not matter: the finding
	// under test is made from the options and no request.
	cs := fake.NewClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, action.(k8stesting.CreateAction).GetObject(), nil
	})
	produced, err := staterecord.CheckClusterContract(context.Background(), cs, staterecord.ClusterContractOptions{
		Namespace: clusterNS, NamespaceKnownToExist: true, InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("CheckClusterContract: %v", err)
	}
	if len(produced) == 0 || produced[0].Setting != staterecord.ClusterTLSVerification {
		t.Fatalf("the contract produced no tls_verification finding for InsecureTLS: %+v", produced)
	}
	return append([]staterecord.Finding{produced[0]}, clusterFindings()...)
}

func TestLiveClusterFailsABlockThatSetsInsecure(t *testing.T) {
	findings := insecureClusterFindings(t)

	for name, rs := range map[string]*configs.LiveRecordStore{
		"no waiver": {Type: "kubernetes"},
		"waived":    {Type: "kubernetes", AllowInsecure: []string{"tls_verification"}},
	} {
		r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings, rs)
		if r.Correct {
			t.Errorf("%s: a cluster reached with verification turned off was reported correct, so live-cluster would exit 0", name)
		}
		if r.Warnings != 0 {
			t.Errorf("%s: the finding was counted as a warning, which exits 0", name)
		}
		if r.Settings[0].Setting != "tls_verification" || r.Settings[0].Verdict != "fail" {
			t.Errorf("%s: the first line is %+v, want tls_verification fail", name, r.Settings[0])
		}
		text := renderLiveClusterReport(r)
		for _, want := range []string{"tls_verification    FAIL", "`insecure = true`", "records namespace " + clusterNS + ": NOT correct"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the report does not contain %q:\n%s", name, want, text)
			}
		}
	}

	waived := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings,
		&configs.LiveRecordStore{Type: "kubernetes", AllowInsecure: []string{"tls_verification"}})
	if len(waived.Waived) != 1 || !waived.Waived[0].Hiding {
		t.Fatalf("the waiver is hiding a real failure and was not reported so: %+v", waived.Waived)
	}
	if text := renderLiveClusterReport(waived); !strings.Contains(text, `allow_insecure names "tls_verification", and the cluster DOES fail it`) {
		t.Errorf("the waiver line is missing:\n%s", text)
	}

	// The argument removed and the waiver left behind: nothing about TLS is
	// reported, and the waiver is named as hiding nothing.
	clean := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(),
		&configs.LiveRecordStore{Type: "kubernetes", AllowInsecure: []string{"tls_verification"}})
	if !clean.Correct {
		t.Error("a cluster with no tls_verification finding was reported NOT correct")
	}
	text := renderLiveClusterReport(clean)
	if strings.HasPrefix(text, "  tls_verification") || strings.Contains(text, "\n  tls_verification") {
		t.Errorf("a block that does not set insecure printed a tls_verification line:\n%s", text)
	}
	if !strings.Contains(text, `allow_insecure names "tls_verification". The cluster passes it today`) {
		t.Errorf("the leftover waiver is not named as hiding nothing:\n%s", text)
	}
	if len(clean.Waived) != 1 || clean.Waived[0].Hiding {
		t.Errorf("the leftover waiver was reported as hiding a failure: %+v", clean.Waived)
	}
}

func TestBeforeApplyRefusesInsecureAndSaysSoWhenWaived(t *testing.T) {
	ctx := context.Background()
	findings := insecureClusterFindings(t)
	runnerOver := func(rs *configs.LiveRecordStore) *statelessRunner {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: findings}
		return &statelessRunner{rawStore: store, recordStoreCfg: rs, recordEstate: "prod"}
	}

	t.Run("not waived refuses by name", func(t *testing.T) {
		rs := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true}
		diags := runnerOver(rs).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("an apply over a connection with verification turned off went ahead")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"The record store cluster fails its tls_verification assertion", "`insecure = true`",
			"cluster_ca_certificate", `allow_insecure = ["tls_verification"]`, "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("waived proceeds and says what it let through", func(t *testing.T) {
		rs := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true,
			AllowInsecure: []string{"tls_verification"}}
		diags := runnerOver(rs).BeforeApply(ctx)
		if diags.HasErrors() {
			t.Fatalf("a waived tls_verification failure refused the apply:\n%s", diags.Err())
		}
		got := details(diags, tfdiags.Warning)
		for _, want := range []string{"The waived tls_verification assertion would have refused this apply",
			"`insecure = true`", `allow_insecure names "tls_verification"`} {
			if !strings.Contains(got, want) {
				t.Errorf("the warning does not say %q:\n%s", want, got)
			}
		}

		// The every-run warning, which a plan prints too, names its own cost.
		every := details(bucketWaiverWarnings(rs), tfdiags.Warning)
		for _, want := range []string{"The record store cluster's tls_verification assertion is waived", "credential"} {
			if !strings.Contains(every, want) {
				t.Errorf("the every-run waiver warning does not say %q:\n%s", want, every)
			}
		}
	})

	t.Run("a waiver of something else does not reach it", func(t *testing.T) {
		rs := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true,
			AllowInsecure: []string{"encryption_at_rest"}}
		if diags := runnerOver(rs).BeforeApply(ctx); !diags.HasErrors() {
			t.Fatal("waiving encryption_at_rest also waived tls_verification")
		}
	})
}

// TestLiveClusterHelpNamesTheFifthLine keeps the help honest about its own
// count: it says "all four", and a block that sets insecure prints five.
func TestLiveClusterHelpNamesTheFifthLine(t *testing.T) {
	help := (&LiveClusterCommand{}).Help()
	for _, want := range []string{"insecure = true", "tls_verification"} {
		if !strings.Contains(help, want) {
			t.Errorf("the help does not mention %q:\n%s", want, help)
		}
	}
}
