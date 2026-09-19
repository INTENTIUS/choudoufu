// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestWithNoRecordAManifestLabelRemovalPlansNothing pins the consequence that
// makes a record store which would not open matter for a kubernetes_manifest
// more than for any other type, and it is the opposite of the consequence
// people expect. GitHub issue #1376.
//
// For a record-backed type, no record means the resource reads as absent and
// the plan proposes a create: wrong, and visible. For a manifest, the record
// holds which labels and annotations this estate DECLARED, and that is the
// only way a plan can tell "the configuration dropped squad" from "somebody
// ran kubectl label". With no record there are no removal candidates, the
// removed key stays out of the prior, the prior agrees with the
// configuration, and the plan says "No changes" while the live object keeps
// the label. Wrong, and silent.
//
// `live-plan` and `live-mv` go on without a store after an outage, so the
// warning they raise has to say this. internal/command holds the warning to
// it; this test holds the behaviour the warning describes, so that if a later
// change makes a manifest removal plan without a record, the warning's
// sentence gets revisited and not just left to rot.
func TestWithNoRecordAManifestLabelRemovalPlansNothing(t *testing.T) {
	live := removalLive()
	block := manifestTypeSchema().Block

	// With the record: squad and owner are candidates, and the prior carries
	// squad's live value, so the provider sees a difference and plans it.
	withRecord, ok := manifestRemovalCandidates(live, recordedEverything())
	if !ok || !withRecord[markers.LabelSurfaceAttr]["squad"] {
		t.Fatalf("premise: with the record, squad is not a removal candidate (%v); this test proves nothing", withRecord)
	}
	if got := priorMapOf(t, mirrorManifestComputedFields(live, block, withRecord), "labels"); got["squad"] == "" {
		t.Fatalf("premise: with the record, the removal does not reach the prior: %v", got)
	}

	// With no record, which is what a store that would not open leaves: the
	// declared-keys lookup is empty.
	noRecord, _ := manifestRemovalCandidates(live, nil)
	if n := len(noRecord[markers.LabelSurfaceAttr]) + len(noRecord[markers.AnnotationSurfaceAttr]); n != 0 {
		t.Fatalf("with no record there are %d removal candidate(s): %v. If a removal can now be planned without a record, the record-store-not-read warning in internal/command describes something that is no longer true", n, noRecord)
	}
	if got := priorMapOf(t, mirrorManifestComputedFields(live, block, noRecord), "labels"); got["squad"] != "" {
		t.Errorf("with no record the removed label reached the prior anyway (%v); the warning in internal/command says it does not", got)
	}
}
