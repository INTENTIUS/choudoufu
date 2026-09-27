// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package k8stest holds the gate and the fixture-reading contract choudoufu's
// Kubernetes live tests share, beside internal/live/flocitest's for the AWS
// side.
//
// Issue #1596: before this package, every CHOUDOUFU_K8S_* test read its own
// environment variable directly and skipped when it was unset - a shape
// with no single switch, and no way for a test to tell "the tier is simply
// not running here" apart from "the tier is running and its own setup step
// forgot to wire one fixture through". The second case is the always-green
// failure mode CLAUDE.md warns about: a nightly workflow that sets some but
// not all of a test's variables would still see nothing but a skip, exactly
// as green as a laptop with no cluster at all.
//
// Gate is the switch (mirroring flocitest.Gate's TF_ACC contract), and
// RequireEnv is what a test calls once it is past the switch: a fixture
// that is still missing at that point is the tier's own setup failing to
// wire something through, and RequireEnv fails the test rather than
// degrading to a skip that reads as a pass.
package k8stest

import (
	"os"
	"strings"
	"testing"
)

// EnvVar is this tier's own opt-in switch, alongside TF_ACC, which every
// live tier shares (see internal/live/flocitest.Gate). Named after
// flocitest's TF_FLOCI_TEST.
const EnvVar = "CHOUDOUFU_K8S_TEST"

// Gate skips t unless the Kubernetes live tier is enabled: either TF_ACC
// (the acceptance-test switch every live tier shares) or CHOUDOUFU_K8S_TEST
// (this tier alone), the same two-switch shape flocitest.Gate gives the
// AWS-backed tests. subject names what is under test and appears in the
// skip message.
//
// A skip here is the ordinary case for a laptop with no cluster, and is not
// itself a finding. What Gate exists to prevent is the tier running with
// this check absent: a test that instead skipped on its own fixture
// variable alone could never tell "nobody asked for this tier" apart from
// "the tier's setup broke and forgot one variable", and both looked exactly
// like a pass.
func Gate(t *testing.T, subject string) {
	t.Helper()

	if os.Getenv("TF_ACC") == "" && os.Getenv(EnvVar) == "" {
		t.Skipf("the %s Kubernetes live test requires setting TF_ACC or %s, and a real Kubernetes API server; see this test's own doc comment for the fixtures it needs", subject, EnvVar)
	}
}

// RequireEnv reads a fixture environment variable a test needs once [Gate]
// has already let it run: a kubeconfig path, a namespace, an impersonated
// identity's name. A test that reached here already opted into the tier, so
// an empty value is the tier's own setup failing to wire something through,
// not a reason to degrade to a skip that a nightly run would report as a
// pass. hint names how the fixture is produced - a scenario, a workflow
// step - so the failure says where to look rather than only what is
// missing.
func RequireEnv(t *testing.T, name, hint string) string {
	t.Helper()

	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("%s is not set even though the Kubernetes live tier is enabled (TF_ACC or %s). %s", name, EnvVar, hint)
	}
	return v
}
