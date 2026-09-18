// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1287. A record write that fails for a record-rung instance
// leaves that instance with NO carrier at all: the record is the ownership
// for a type that has nowhere to hang a tag, so an unwritten record is a
// live object nothing claims, and the first live-plan after the migration
// proposes creating a second copy of it.
//
// recordOne has always said exactly that in its Detail - "Nothing was
// written, and the first live-plan after this migration will propose
// creating it" - and the run it says it in exited 0, because a StampOutcome
// row is not a diagnostic. An operator migrating five hundred instances
// reads a summary line, gets a zero exit, and believes the estate is
// migrated. The plan that proposes the duplicate arrives later, possibly on
// another machine, for someone who never saw the row.
//
// This is the half of #1287 that no in-process mechanism can reach. The
// ledger in internal/live/projection stops a LATER READ IN THIS RUN from
// mistaking "could not be written" for "is not there"; it cannot help a
// separate process a week later, where the store is healthy and simply holds
// nothing. What stands in front of that plan is this run failing.

// outageStore refuses every content write, the way a store behind an S3
// outage, a denied IAM action or an expired credential does. Reads and
// enumeration are untouched, which is what makes the failure quiet: nothing
// afterwards has any trouble at all.
type outageStore struct {
	staterecord.Store
}

var errStoreOutage = errors.New("simulated record-store outage")

func (s *outageStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	return "", errStoreOutage
}

func (s *outageStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "", errStoreOutage
}

// TestApprove_AFailedRecordWriteFailsTheMigration is the decisive arm for
// the cross-run half: the migration must not report success over an instance
// whose only carrier it could not write.
func TestApprove_AFailedRecordWriteFailsTheMigration(t *testing.T) {
	addr := mustAddr(t, "random_pet.this")
	store := &outageStore{Store: petStore(t)}

	rep, diags := petRatify(t, store, petState(petName), newPetProvider()).Approve(context.Background())

	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeFailed {
		t.Fatalf("the premise failed: outcome = %s, want %s: %s", out.Outcome, OutcomeFailed, out.Detail)
	}
	// The cost, measured rather than asserted about: the store holds nothing
	// for this address, so the next plan has nothing to bind random_pet.this
	// to and will propose creating it.
	if got := recordedID(t, store, addr); got != "" {
		t.Fatalf("the premise failed: the store holds %q after a run in which every write was refused", got)
	}

	if !diags.HasErrors() {
		t.Fatalf("Approve reported no error for an instance whose record could not be written, so the command "+
			"exits 0 and the operator is told the estate is migrated; the outcome row said %q and nothing else "+
			"carried it", out.Detail)
	}
	if !strings.Contains(diags.Err().Error(), addr.String()) {
		t.Errorf("the error does not name %s: %s", addr, diags.Err())
	}
	if !strings.Contains(diags.Err().Error(), errStoreOutage.Error()) {
		t.Errorf("the error does not name what stopped the write: %s", diags.Err())
	}
}

// TestApprove_ARefusedOverwriteIsStillNotAnError is the boundary, and the
// reason the check above asks the record store what happened rather than
// keying off OutcomeFailed.
//
// TestApprove_NeverOverwritesAnExistingRecord covers the same ground from
// the other direction; this states the rule in #1287's own terms. A
// migration that declined to overwrite a record because the store already
// holds a DIFFERENT value wrote nothing either - but the record is there,
// the instance has a carrier, and no later plan proposes creating anything.
// That is a report row, not a failed run, and turning every OutcomeFailed
// into an error would have made it one.
func TestApprove_ARefusedOverwriteIsStillNotAnError(t *testing.T) {
	store := petStore(t)
	if _, diags := petRatify(t, store, petState("the-live-value"), newPetProvider()).Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("seeding the first value returned errors: %s", diags.Err())
	}

	rep, diags := petRatify(t, store, petState("a-stale-tfstates-value"), newPetProvider()).Approve(context.Background())
	if got := onlyOutcome(t, rep).Outcome; got != OutcomeFailed {
		t.Fatalf("the premise failed: outcome = %s, want %s", got, OutcomeFailed)
	}
	if diags.HasErrors() {
		t.Errorf("Approve failed the run over a record it deliberately left alone, which is a refusal to "+
			"overwrite and not a missing carrier: %s", diags.Err())
	}
}
