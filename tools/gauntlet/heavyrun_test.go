// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"strings"
	"testing"
)

// TestWithDispatchHintNil proves the wrapper is a true no-op on success: a
// caller that always wraps CheckMaintainerAllow's return value must not
// turn a nil (allowed) result into a non-nil one.
func TestWithDispatchHintNil(t *testing.T) {
	if got := withDispatchHint(nil, "live-cert.yml", "irrelevant"); got != nil {
		t.Errorf("withDispatchHint(nil, ...) = %v, want nil", got)
	}
}

// TestWithDispatchHintNamesWorkflowAndDispatchLine is the actual contract
// item 3 of this unit asks for: the refusal a maintainer or an agent sees
// must name the workflow file and the exact `gh workflow run` line, on top
// of whatever the underlying guard already said.
func TestWithDispatchHintNamesWorkflowAndDispatchLine(t *testing.T) {
	base := errors.New("refusing: the allow file does not exist")
	dispatchLine := "gh workflow run live-cert.yml -R INTENTIUS/choudoufu -f estate=reference-ec2-vpc -f scale=1 -f ceiling_usd=15"

	got := withDispatchHint(base, "live-cert.yml", dispatchLine)
	if got == nil {
		t.Fatal("withDispatchHint(non-nil error, ...) = nil, want a wrapped refusal")
	}
	msg := got.Error()
	for _, want := range []string{
		"refusing: the allow file does not exist", // the original reason is preserved
		"live-cert.yml", // the workflow file
		dispatchLine,    // the exact dispatch line
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("wrapped refusal missing %q: %q", want, msg)
		}
	}

	// The wrap must be a real error-wrap, not just string concatenation, so
	// errors.Is/As still sees the original cause.
	if !errors.Is(got, base) {
		t.Error("withDispatchHint's result does not wrap the original error (errors.Is failed) - it must wrap the base error, not just format it into a new string")
	}
}
