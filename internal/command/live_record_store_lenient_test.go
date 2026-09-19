// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

func openerReturning(store staterecord.Store, err error) recordStoreOpener {
	return func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
		return store, err
	}
}

func summaries(diags tfdiags.Diagnostics, sev tfdiags.Severity) []string {
	var out []string
	for _, d := range diags {
		if d.Severity() == sev {
			out = append(out, d.Description().Summary)
		}
	}
	return out
}

// TestLivePlanAndLiveMvStopOnARefusalAndGoOnLoudlyAfterAnOutage is GitHub
// issue #1376. Both commands used to write every failure to open the record
// store to the log and carry on, so a bucket the contract refused on first
// contact was planned against, and a KMS refusal looked like a quiet day.
func TestLivePlanAndLiveMvStopOnARefusalAndGoOnLoudlyAfterAnOutage(t *testing.T) {
	ctx := context.Background()
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a refused bucket stops the command", func(t *testing.T) {
		refusal := &projection.StoreRefusal{Err: errors.New("The record store bucket fails its versioning assertion")}
		store, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(nil, refusal), rs, nil, "prod", "live-plan")
		if store != nil || !diags.HasErrors() {
			t.Fatalf("store=%v errors=%v, want no store and an error", store, diags.HasErrors())
		}
		if !strings.Contains(diags.Err().Error(), "versioning assertion") {
			t.Errorf("the error does not carry the refusal: %v", diags.Err())
		}
	})

	t.Run("a KMS refusal stops the command, under its own summary", func(t *testing.T) {
		kms := fmt.Errorf("opening: %w", &staterecord.KMSDeniedError{Action: "kms:Decrypt", Err: errors.New("AccessDenied")})
		_, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(nil, kms), rs, nil, "prod", "live-mv")
		if got := summaries(diags, tfdiags.Error); len(got) != 1 || got[0] != "The record store bucket's KMS key refused this run" {
			t.Errorf("error summaries = %q", got)
		}
	})

	t.Run("an outage is a warning a person sees, and the command goes on", func(t *testing.T) {
		store, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(nil, errors.New("connection refused")), rs, nil, "prod", "live-plan")
		if store != nil || diags.HasErrors() {
			t.Fatalf("store=%v errors=%v, want no store and no error", store, diags.HasErrors())
		}
		got := summaries(diags, tfdiags.Warning)
		if len(got) != 1 || got[0] != SummaryRecordStoreNotRead {
			t.Fatalf("warning summaries = %q, want exactly %q", got, SummaryRecordStoreNotRead)
		}
		detail := diags[0].Description().Detail
		for _, want := range []string{"live-plan", "connection refused", "may appear here as something to create", "`plan` and `apply` do not go on"} {
			if !strings.Contains(detail, want) {
				t.Errorf("the warning does not say %q:\n%s", want, detail)
			}
		}
	})

	t.Run("an invalid parallelism value stops the command", func(t *testing.T) {
		t.Setenv(recordReadParallelismEnvVar, "lots")
		called := false
		open := func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
			called = true
			return local, nil
		}
		_, diags := openRecordStoreAsOneMoreSource(ctx, open, rs, nil, "prod", "live-plan")
		if !diags.HasErrors() {
			t.Error("an invalid value was accepted")
		}
		if called {
			t.Error("the store was opened with a value the operator set and this run could not read")
		}
	})

	t.Run("a waiver is announced here as on every other path", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"lifecycle"}}
		store, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(local, nil), waived, nil, "prod", "live-mv")
		if store == nil || diags.HasErrors() {
			t.Fatalf("store=%v errors=%v", store, diags.HasErrors())
		}
		if got := summaries(diags, tfdiags.Warning); len(got) != 1 || !strings.Contains(got[0], "lifecycle assertion is waived") {
			t.Errorf("warning summaries = %q", got)
		}
	})

	t.Run("a store that opens with nothing waived says nothing", func(t *testing.T) {
		store, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(local, nil), rs, nil, "prod", "live-plan")
		if store == nil || len(diags) != 0 {
			t.Errorf("store=%v diags=%d", store, len(diags))
		}
	})

	t.Run("no record store configured", func(t *testing.T) {
		store, diags := openRecordStoreAsOneMoreSource(ctx, openerReturning(local, nil), nil, nil, "prod", "live-plan")
		if store != nil || len(diags) != 0 {
			t.Errorf("store=%v diags=%d", store, len(diags))
		}
	})
}
