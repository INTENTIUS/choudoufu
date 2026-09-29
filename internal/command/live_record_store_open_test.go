// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

func TestRecordStoreOpenDiagNamesAKMSRefusal(t *testing.T) {
	denied := &staterecord.KMSDeniedError{
		Action:    "kms:GenerateDataKey",
		KeyARN:    "arn:aws:kms:us-east-2:111122223333:key/k",
		Principal: "arn:aws:sts::111122223333:assumed-role/estate/run",
		Where:     staterecord.KMSDeniedByKeyPolicy,
		Err:       errors.New("api error AccessDenied"),
	}
	// Wrapped the way the store and the sentinel provisioning wrap it.
	d := recordStoreOpenDiag("s3", fmt.Errorf("record_store: provisioning the sentinel: %w", fmt.Errorf("staterecord: s3: writing %q: %w", "k", denied)))
	if got := d.Description().Summary; got != "The record store bucket's KMS key refused this run" {
		t.Errorf("summary = %q", got)
	}
	for _, want := range []string{denied.KeyARN, "kms:GenerateDataKey", "assumed-role/estate", "Add the estate's role to the key policy"} {
		if !strings.Contains(d.Description().Detail, want) {
			t.Errorf("the detail does not say %q:\n%s", want, d.Description().Detail)
		}
	}

	// The headline and the remedy appear once. The detail used to append the
	// whole wrapped error, which is the headline and the remedy again.
	if n := strings.Count(d.Description().Detail, "refused kms:GenerateDataKey"); n != 1 {
		t.Errorf("the headline appears %d time(s) in the detail, want 1:\n%s", n, d.Description().Detail)
	}
	if !strings.Contains(d.Description().Detail, "api error AccessDenied") {
		t.Errorf("the detail lost what S3 actually said:\n%s", d.Description().Detail)
	}

	plain := recordStoreOpenDiag("s3", errors.New("api error AccessDenied: s3:GetObject"))
	if got := plain.Description().Summary; got != "Cannot open the record store" {
		t.Errorf("an error that is not a KMS refusal got the summary %q", got)
	}
	if strings.Contains(plain.Description().Detail, "KMS") {
		t.Errorf("an error that is not a KMS refusal mentions KMS:\n%s", plain.Description().Detail)
	}
}

// TestRecordStoreOpenDiagNamesAnUnusableKMSKey is GitHub issue #1383. A key
// that is disabled, pending deletion or gone is not a permission problem,
// and the refusal above sends the reader to the key policy, where a disabled
// key looks entirely correct.
func TestRecordStoreOpenDiagNamesAnUnusableKMSKey(t *testing.T) {
	for _, tc := range []struct {
		code   string
		remedy string
	}{
		{"KMS.DisabledException", "The key is disabled."},
		{"KMS.KMSInvalidStateException", "pending deletion"},
		{"KMS.NotFoundException", "does not exist in this account and region"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			unusable := &staterecord.KMSKeyUnusableError{
				Code:   tc.code,
				KeyARN: "arn:aws:kms:us-east-2:111122223333:key/k",
				Err:    fmt.Errorf("api error %s: the key is not usable", tc.code),
			}
			// Wrapped the way the store and the sentinel provisioning wrap it.
			d := recordStoreOpenDiag("s3", fmt.Errorf("record_store: provisioning the sentinel: %w", fmt.Errorf("staterecord: s3: writing %q: %w", "k", unusable)))
			desc := d.Description()
			if desc.Summary != "The record store bucket's KMS key cannot be used" {
				t.Errorf("summary = %q", desc.Summary)
			}
			for _, want := range []string{unusable.KeyARN, tc.code, tc.remedy} {
				if !strings.Contains(desc.Detail, want) {
					t.Errorf("the detail does not say %q:\n%s", want, desc.Detail)
				}
			}
			// None of this type's remedies is a policy edit, and saying so
			// here is the whole point of the separate summary.
			for _, never := range []string{"key policy", "IAM policy"} {
				if strings.Contains(desc.Detail, never) {
					t.Errorf("the detail sends the reader to %q for a key whose STATE is the problem:\n%s", never, desc.Detail)
				}
			}
			// Headline and remedy once each: the detail used to append the
			// whole wrapped error, which carries both again.
			if n := strings.Count(desc.Detail, "could not be used"); n != 1 {
				t.Errorf("the headline appears %d time(s) in the detail, want 1:\n%s", n, desc.Detail)
			}
			if n := strings.Count(desc.Detail, "the key is not usable"); n != 1 {
				t.Errorf("what S3 said appears %d time(s) in the detail, want 1:\n%s", n, desc.Detail)
			}
		})
	}
}
