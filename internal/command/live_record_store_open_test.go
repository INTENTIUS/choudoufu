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

	plain := recordStoreOpenDiag("s3", errors.New("api error AccessDenied: s3:GetObject"))
	if got := plain.Description().Summary; got != "Cannot open the record store" {
		t.Errorf("an error that is not a KMS refusal got the summary %q", got)
	}
	if strings.Contains(plain.Description().Detail, "KMS") {
		t.Errorf("an error that is not a KMS refusal mentions KMS:\n%s", plain.Description().Detail)
	}
}
