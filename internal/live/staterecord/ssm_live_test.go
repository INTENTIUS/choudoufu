// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This is the real-wire counterpart to ssm_test.go's fake-server suite:
// the same [Store] conformance run, but against a real SSM Parameter
// Store served by floci rather than a handler this package wrote itself.
// floci is known to serve ssm:PutParameter — live/SURVEY.md's roster
// records aws_ssm_parameter as "wired" against it — so this exists to
// prove [SSMStore] itself speaks the real wire protocol correctly, not
// just this test file's own idea of what that protocol looks like.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/staterecord/ -run TestSSMStoreAgainstFloci -v
//
// It is gated because it needs Docker and about a minute; see
// requireDockerDaemon for why this checks more than just the binary being
// on PATH.
func TestSSMStoreAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "staterecord/ssm")
	flocitest.RequireBinary(t, "docker")
	requireDockerDaemon(t)

	port := flocitest.StartFloci(t, "staterecord-ssm")
	endpoint := flocitest.Endpoint(port)

	client := ssm.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *ssm.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, err := NewSSMStore(SSMConfig{Client: client, KeyPrefix: "/choudoufu-staterecord-test/" + randomKeySegment(t)})
		if err != nil {
			t.Fatalf("NewSSMStore: %v", err)
		}
		return store
	})
}

// requireDockerDaemon skips t when the docker daemon is not reachable,
// distinct from flocitest.RequireBinary's PATH-only check: `docker` can be
// installed but its daemon not running (a common state on a CI box or a
// laptop that has not opened Docker Desktop), and `docker info` is the
// standard way to tell the two apart before spending a StartFloci attempt
// on a daemon that was never going to answer.
func requireDockerDaemon(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker info failed, so the docker daemon is not usable here: %v\n%s", err, out)
	}
}

// randomKeySegment gives each runConformance subtest call its own SSM
// parameter-name hierarchy, so unrelated subtests sharing one floci
// container's Parameter Store never collide on a key like "k1" the way
// [LocalStore]'s per-call t.TempDir() already guarantees for free.
func randomKeySegment(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generating a random key segment: %v", err)
	}
	return hex.EncodeToString(b[:])
}
