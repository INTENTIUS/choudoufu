// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"slices"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestTheWaivableNamesAreTheAssertedSettings: internal/configs cannot import
// the store package to take the names from it, so the two lists are held
// together here. A fourth assertion that could not be waived, or a waiver
// naming nothing, would both be silent.
func TestTheWaivableNamesAreTheAssertedSettings(t *testing.T) {
	var asserted []string
	for _, s := range staterecord.BucketSettings {
		asserted = append(asserted, string(s))
	}
	if !slices.Equal(asserted, configs.RecordStoreInsecureSettings) {
		t.Errorf("staterecord asserts %v, allow_insecure accepts %v", asserted, configs.RecordStoreInsecureSettings)
	}
}

// TestAWaiverWarnsByNameWithItsCostInTheSameSentence is GitHub issue #1340's
// fifth acceptance item.
func TestAWaiverWarnsByNameWithItsCostInTheSameSentence(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"versioning", "public_access_block"}}
	diags := bucketWaiverWarnings(rs)
	if len(diags) != 2 {
		t.Fatalf("got %d warnings for 2 waived settings", len(diags))
	}
	for i, name := range rs.AllowInsecure {
		d := diags[i]
		if d.Severity() != tfdiags.Warning {
			t.Errorf("%s: severity %v, want a warning - a waiver lets the run proceed", name, d.Severity())
		}
		desc := d.Description()
		if !strings.Contains(desc.Summary, name) {
			t.Errorf("the headline %q does not name %q", desc.Summary, name)
		}
		cost := staterecord.BucketWaiverCost(staterecord.BucketSetting(name))
		sentence := ""
		for _, s := range strings.SplitAfter(desc.Detail, ". ") {
			if strings.Contains(s, cost) {
				sentence = s
			}
		}
		if sentence == "" || !strings.Contains(sentence, `"`+name+`"`) {
			t.Errorf("no single sentence of the detail carries both %q and its cost: %q", name, desc.Detail)
		}
		if !strings.Contains(desc.Detail, "the-bucket") {
			t.Errorf("the warning does not name the bucket: %q", desc.Detail)
		}
	}
	if got := bucketWaiverWarnings(&configs.LiveRecordStore{Type: "s3", Bucket: "b"}); len(got) != 0 {
		t.Errorf("no waiver configured, %d warning(s) emitted", len(got))
	}
	if got := bucketWaiverWarnings(nil); len(got) != 0 {
		t.Errorf("no record store at all, %d warning(s) emitted", len(got))
	}
}
