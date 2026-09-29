// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

// GitHub issue #1448, section C. `insecure = true` on a record_store
// "kubernetes" block used to be accepted with no finding and no waiver name.
// It is tls_verification now, and these are its three arms: set and not
// waived refuses by name, set and waived is a waived failure that stays in
// the run's hands to say out loud, and not set produces nothing at all.

// insecureCluster is a cluster whose reviews all answer yes inside the
// records namespace, reached over a connection the block made insecure.
func insecureCluster(t *testing.T, insecure bool) []Finding {
	t.Helper()
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return ns == contractNamespace })
	return check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, InsecureTLS: insecure})
}

func hasSetting(findings []Finding, setting Setting) bool {
	for _, f := range findings {
		if f.Setting == setting {
			return true
		}
	}
	return false
}

func TestInsecureTLSRefusesByNameWithoutAWaiver(t *testing.T) {
	findings := insecureCluster(t, true)
	f := findings[0]
	if f.Setting != ClusterTLSVerification {
		t.Fatalf("the first finding is %q, want %q: every other answer was read over that connection", f.Setting, ClusterTLSVerification)
	}
	if f.OK() || f.Outcome != Failed {
		t.Fatalf("insecure = true came out as outcome %d, want Failed: it was read off the block and it is wrong", f.Outcome)
	}
	if f.Unwaivable {
		t.Error("the finding is unwaivable, and the ruling is that allow_insecure reaches it")
	}
	if want := "`insecure = true` is set, so the API server's certificate is not verified"; f.Found != want {
		t.Errorf("the finding reads %q, want %q", f.Found, want)
	}

	// A waiver of something else does not reach it.
	refused, warned, waived := SplitWaived(findings, []string{"encryption_at_rest", "estate_boundary", "read_isolation", "namespace_access"})
	if !hasSetting(refused, ClusterTLSVerification) {
		t.Fatalf("every other name was waived and tls_verification was not refused: refused %v, warned %v, waived %v",
			settingsOf(refused), settingsOf(warned), settingsOf(waived))
	}

	summary, detail := ClusterContractRefusal(contractNamespace, f)
	if !strings.Contains(summary, "tls_verification") {
		t.Errorf("the headline does not carry the name: %q", summary)
	}
	for _, want := range []string{
		"With verification off, anything on the path can answer as the API server, and it receives this identity's credential and every record.",
		"Remove `insecure = true` and set `cluster_ca_certificate`.",
		`allow_insecure = ["tls_verification"]`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not carry %s: %q", want, detail)
		}
	}
}

func TestInsecureTLSWaivedIsAWaivedFailureAndNotAPass(t *testing.T) {
	findings := insecureCluster(t, true)
	refused, warned, waived := SplitWaived(findings, []string{"tls_verification"})
	if hasSetting(refused, ClusterTLSVerification) || hasSetting(warned, ClusterTLSVerification) {
		t.Fatalf("allow_insecure names tls_verification and it still refused or warned: refused %v, warned %v", settingsOf(refused), settingsOf(warned))
	}
	if !hasSetting(waived, ClusterTLSVerification) {
		t.Fatalf("the waived finding is not among the waived failures (%v), so no run could say it proceeded past it", settingsOf(waived))
	}
	if cost, want := ClusterWaiverCost(ClusterTLSVerification), "the API server's certificate is not verified, so this identity's credential and every record go to whatever answers at that address"; cost != want {
		t.Errorf("the waiver's cost reads %q, want %q", cost, want)
	}
}

func TestVerifiedTLSProducesNoFinding(t *testing.T) {
	findings := insecureCluster(t, false)
	if hasSetting(findings, ClusterTLSVerification) {
		t.Fatalf("a block that does not set insecure = true carries a tls_verification finding: %v", settingsOf(findings))
	}
	// A waiver left behind after the argument was removed hides nothing.
	_, _, waived := SplitWaived(findings, []string{"tls_verification"})
	if hasSetting(waived, ClusterTLSVerification) {
		t.Error("a waiver with nothing to waive was reported as hiding a failure")
	}
}

// TestInsecureTLSReachesTheContractThroughTheStore is the path a run takes:
// internal/live/projection builds the store from the block and asks the store
// for its contract, so the store is what has to carry the block's argument.
func TestInsecureTLSReachesTheContractThroughTheStore(t *testing.T) {
	for _, insecure := range []bool{true, false} {
		cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return ns == contractNamespace })
		store, err := NewKubernetesStore(KubernetesConfig{
			Secrets:     cs.CoreV1().Secrets(contractNamespace),
			Clientset:   cs,
			Namespace:   contractNamespace,
			Estate:      "alice",
			InsecureTLS: insecure,
		})
		if err != nil {
			t.Fatalf("NewKubernetesStore: %v", err)
		}
		checker, ok := AsContractChecker(store)
		if !ok {
			t.Fatal("the kubernetes store has no contract")
		}
		findings, err := checker.CheckContract(context.Background(), ContractOptions{})
		if err != nil {
			t.Fatalf("CheckContract: %v", err)
		}
		if got := hasSetting(findings, ClusterTLSVerification); got != insecure {
			t.Errorf("InsecureTLS=%v: tls_verification finding present = %v", insecure, got)
		}
		refused, _, _ := SplitWaived(findings, nil)
		text := ContractRefusalText(checker, refused)
		if got := strings.Contains(text, "fails its tls_verification assertion"); got != insecure {
			t.Errorf("InsecureTLS=%v: the refusal text names tls_verification = %v", insecure, got)
		}
	}
}
