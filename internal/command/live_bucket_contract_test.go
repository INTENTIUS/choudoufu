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
	if !slices.Equal(asserted, configs.RecordStoreInsecureSettingsFor("s3")) {
		t.Errorf("record_store \"s3\" accepts %v, want the asserted settings %v", configs.RecordStoreInsecureSettingsFor("s3"), asserted)
	}

	// The same pin on the cluster contract (GitHub issue #1393).
	var clusterAsserted []string
	for _, s := range staterecord.ClusterSettings {
		clusterAsserted = append(clusterAsserted, string(s))
	}
	if !slices.Equal(clusterAsserted, configs.RecordStoreClusterInsecureSettings) {
		t.Errorf("staterecord asserts %v about a cluster, allow_insecure accepts %v", clusterAsserted, configs.RecordStoreClusterInsecureSettings)
	}
	if !slices.Equal(clusterAsserted, configs.RecordStoreInsecureSettingsFor("kubernetes")) {
		t.Errorf("record_store \"kubernetes\" accepts %v, want the asserted settings %v", configs.RecordStoreInsecureSettingsFor("kubernetes"), clusterAsserted)
	}

	// The two vocabularies must not overlap. A bucket setting named on a
	// cluster store has to be refused by name, not silently waive the
	// cluster setting that happens to sit at the same index.
	for _, name := range configs.RecordStoreInsecureSettings {
		if slices.Contains(configs.RecordStoreClusterInsecureSettings, name) {
			t.Errorf("%q names an assertion of both stores, so a reader cannot tell which one a waiver waives", name)
		}
	}

	// The local store asserts nothing, so it waives nothing.
	if got := configs.RecordStoreInsecureSettingsFor("local"); got != nil {
		t.Errorf("record_store \"local\" accepts allow_insecure names %v; a directory has nothing to assert", got)
	}
}

// TestAClusterWaiverWarnsByNameWithItsCostInTheSameSentence is #1340's fifth
// acceptance item on the cluster contract (GitHub issue #1393). The same
// rule, the same shape, its own names.
func TestAClusterWaiverWarnsByNameWithItsCostInTheSameSentence(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "kubernetes", Namespace: "tofu-records-alice", NamespaceSet: true,
		AllowInsecure: []string{"encryption_at_rest", "estate_boundary"}}
	diags := bucketWaiverWarnings(rs)
	if len(diags) != 2 {
		t.Fatalf("got %d warnings for 2 waived settings", len(diags))
	}
	for i, name := range rs.AllowInsecure {
		desc := diags[i].Description()
		if diags[i].Severity() != tfdiags.Warning {
			t.Errorf("%s: severity %v, want a warning", name, diags[i].Severity())
		}
		if !strings.Contains(desc.Summary, name) {
			t.Errorf("the headline %q does not name %q", desc.Summary, name)
		}
		if strings.Contains(desc.Summary, "bucket") {
			t.Errorf("a cluster waiver warns about a bucket: %q", desc.Summary)
		}
		cost := staterecord.ClusterWaiverCost(staterecord.Setting(name))
		sentence := ""
		for _, s := range strings.SplitAfter(desc.Detail, ". ") {
			if strings.Contains(s, cost) {
				sentence = s
			}
		}
		if sentence == "" || !strings.Contains(sentence, `"`+name+`"`) {
			t.Errorf("no single sentence of the detail carries both %q and its cost: %q", name, desc.Detail)
		}
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
		cost := staterecord.BucketWaiverCost(staterecord.Setting(name))
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
