// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestDay2ReplaceKindReasonIsScopedToSameNameReplacements is issue #1541's
// docs/stage-reason fix, ruled 2026-09-26.
//
// day2_replace's kind substrate note read: "A Kubernetes name is unique
// within its namespace, so nothing can be created before the object it
// replaces is destroyed; a forced replacement is destroy-then-create,
// which this stage does not measure." That is true of a replacement that
// keeps the object's name. It is false of one that changes it: a
// create_before_destroy rename (the content-hashed-ConfigMap pattern,
// name = "cfg-${sha}") resolves to a different live object under a new
// address, and stock DOES create the new one before destroying the old
// (#1541). The old wording claimed the stage does not apply to Kubernetes
// replacements at all, when it only fails to apply to same-name ones.
//
// Proven red first against the unfixed stages.go (2026-09-26): the note's
// first sentence carries no name/rename qualifier, so both substring
// checks below failed:
//
//	day2replacekindreason_test.go:44: day2_replace kind n/a reason does not
//	    scope itself to same-name replacements: "A Kubernetes name is
//	    unique within its namespace, so nothing can be created before the
//	    object it replaces is destroyed; a forced replacement is
//	    destroy-then-create, which this stage does not measure."
//	day2replacekindreason_test.go:50: day2_replace kind n/a reason does not
//	    point at the tracked rename-order issue (#1541)
func TestDay2ReplaceKindReasonIsScopedToSameNameReplacements(t *testing.T) {
	var stage Stage
	found := false
	for _, s := range Stages() {
		if s.ID == "day2_replace" {
			stage = s
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no day2_replace stage in Stages()")
	}

	reason, na := stage.NotApplicable(SubstrateKind)
	if !na {
		t.Fatalf("day2_replace on %s: want n/a (ruling waits on #1605 to switch it on), got applicable", SubstrateKind)
	}

	lower := strings.ToLower(reason)
	if !strings.Contains(lower, "same name") && !strings.Contains(lower, "same-name") {
		t.Errorf("day2_replace kind n/a reason does not scope itself to same-name replacements: %q", reason)
	}
	if !strings.Contains(reason, "1541") {
		t.Errorf("day2_replace kind n/a reason does not point at the tracked rename-order issue (#1541)")
	}
	// The old, unqualified claim must not survive verbatim: it read as
	// true of every replacement, including a rename.
	stale := "so nothing can be created before the object it replaces is destroyed; a forced replacement is destroy-then-create, which this stage does not measure."
	if strings.Contains(reason, stale) {
		t.Errorf("day2_replace kind n/a reason still carries the unqualified claim: %q", reason)
	}
}
