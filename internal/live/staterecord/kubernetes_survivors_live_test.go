// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"testing"
)

// TestKubernetesListReadsEveryPageOnARealAPIServer is M13r against the thing
// that actually paginates. GitHub issue #1448, section F.
//
// The unit case beside this one (TestKubernetesListReadsEveryPage) runs over
// a SecretInterface double, because client-go's fake clientset ignores Limit
// and Continue: measured, a List carrying Limit 2 over seven Secrets returns
// all seven and an empty continue token. A double that pages honestly is
// still a double, and what a continue token means - that the API server held
// a consistent read across four round trips, and that the store sent back the
// token it was given rather than one it made up - is only true of a real API
// server. So the same seven records and the same page size run here too.
//
// Seven and two are the audit's own numbers: four pages, with the first
// holding two of seven, so a loop that stops after one page is short by five
// and no arithmetic accident can make that look right.
func TestKubernetesListReadsEveryPageOnARealAPIServer(t *testing.T) {
	secrets, _, ns := liveKubernetesSecrets(t)
	prefix := "paging-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })

	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:      secrets,
		Namespace:    ns,
		KeyPrefix:    prefix,
		Estate:       "conformance",
		ListPageSize: 2,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}

	ctx := context.Background()
	want := map[string]string{}
	for i := 0; i < 7; i++ {
		key := fmt.Sprintf("tofu-records/conformance/aws_thing/rec-%02d", i)
		payload := fmt.Sprintf("payload-%02d", i)
		if _, err := store.PutIfAbsent(ctx, key, []byte(payload)); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
		want[key] = payload
	}

	keys, err := store.List(ctx, "tofu-records/conformance/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != len(want) {
		t.Errorf("List returned %d keys over a page size of 2, want all %d: %v", len(keys), len(want), keys)
	}
	for _, key := range keys {
		if _, ok := want[key]; !ok {
			t.Errorf("List returned %q, which this case never wrote", key)
		}
	}
	for key := range want {
		found := false
		for _, got := range keys {
			if got == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("List is missing %q; a page after the first was dropped", key)
		}
	}

	// GetAll reads the same pages and carries every payload, so a dropped
	// page there is a record the caller reads as absent rather than merely
	// unlisted.
	all, err := store.GetAll(ctx, "tofu-records/conformance/")
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != len(want) {
		t.Errorf("GetAll returned %d records over a page size of 2, want all %d", len(all), len(want))
	}
	for key, payload := range want {
		rec, ok := all[key]
		if !ok {
			t.Errorf("GetAll is missing %q; a page after the first was dropped", key)
			continue
		}
		if string(rec.Payload) != payload {
			t.Errorf("GetAll[%q].Payload = %q, want %q", key, rec.Payload, payload)
		}
		if rec.Version == "" {
			t.Errorf("GetAll[%q] carries no version", key)
		}
	}
}
