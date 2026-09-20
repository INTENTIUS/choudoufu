// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #1355. One `apply -destroy` of a two-instance record-backed
// estate destroyed one instance and printed a success; the run kept no log,
// so which of the two halves went wrong - a short prior state, or an apply
// that skipped an instance - could not be read off anything.
//
// It can be read off the code. A destroy plan is built from prior state, and
// prior state for a record-backed instance is one [staterecord.Store.Get].
// That Get answering "no record" with no error is the whole of the silent
// route: [builder.materializeRecord] omits the instance as [ReasonAbsent],
// and a destroy over prior state with an instance missing from it proposes
// nothing for that instance and reports what it did do as a success.
//
// The fixes that keep the bulk read from producing such an answer are in
// internal/live/staterecord. This is the backstop for every other way one
// can arrive: whatever produced it, the store's own listing still names the
// key, and one store answering both ways about one key is a refusal.

// absentForKeyStore answers Get for one key as though no record were there,
// while List keeps naming it - a store contradicting itself about one key,
// which is what a spurious 404 between the listing and the read produces on
// the per-key path. Everything else passes straight through to a real store.
type absentForKeyStore struct {
	staterecord.Store
	hide string
}

func (s *absentForKeyStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if key == s.hide {
		return nil, "", false, nil
	}
	return s.Store.Get(ctx, key)
}

// TestARecordTheListNamesAndTheReadDoesNotIsRefused is the decisive arm. The
// instance is DECLARED and record-backed, its record is in the store, and the
// read of that record comes back absent. Before this check the build was
// clean: one instance materialized, the other omitted as "not created yet",
// no error and no warning.
func TestARecordTheListNamesAndTheReadDoesNotIsRefused(t *testing.T) {
	ctx := context.Background()
	cfg := loadConfig(t, writeTwoNullResourceFixture(t))
	kept := mustAddr(t, `null_resource.trigger["kept"]`)
	missed := mustAddr(t, `null_resource.trigger["missed"]`)

	const prefix = "tofu-records/test-estate"
	backing, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	seedRecordObject(t, backing, prefix, kept)
	seedRecordObject(t, backing, prefix, missed)

	hidden := RecordKey(prefix, missed)
	store := NewRecordEnvelopeStore(&absentForKeyStore{Store: backing, hide: hidden}, prefix)

	provs := SingleProvider(nullProvider, nullResourceProvider())
	resolutions := []identity.Resolution{
		{Addr: kept, Class: identity.ClassRecordBacked},
		{Addr: missed, Class: identity.ClassRecordBacked},
	}

	_, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: store})
	if !diags.HasErrors() {
		t.Fatalf("the build is clean although one read of the store says %s has a record and another says it has none; that answer is prior state with an instance missing from it, and a destroy planned from it destroys one of two and says so as a success:\n%s", missed, renderDiags(diags))
	}
	rendered := renderDiags(diags)
	if !hasDiag(diags, "The record store contradicts itself about a record", hidden) {
		t.Errorf("wrong diagnostics - the refusal must name the key:\n%s", rendered)
	}
	if !strings.Contains(rendered, missed.String()) {
		t.Errorf("the refusal does not name the instance %s:\n%s", missed, rendered)
	}
}

// TestAnAbsentRecordWithNoKeyInTheStoreStillPlansACreate is the control, and
// it is the one that keeps the check above from being a check that fires on
// everything. An instance with genuinely no record - the ordinary
// not-created-yet case, on every first apply of every record-backed resource
// - has no key in the listing either, so the two reads agree and the build
// stays clean.
func TestAnAbsentRecordWithNoKeyInTheStoreStillPlansACreate(t *testing.T) {
	ctx := context.Background()
	cfg := loadConfig(t, writeTwoNullResourceFixture(t))
	kept := mustAddr(t, `null_resource.trigger["kept"]`)
	fresh := mustAddr(t, `null_resource.trigger["missed"]`)

	const prefix = "tofu-records/test-estate"
	backing, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	seedRecordObject(t, backing, prefix, kept)

	store := NewRecordEnvelopeStore(backing, prefix)
	provs := SingleProvider(nullProvider, nullResourceProvider())
	resolutions := []identity.Resolution{
		{Addr: kept, Class: identity.ClassRecordBacked},
		{Addr: fresh, Class: identity.ClassRecordBacked},
	}

	res, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{kept.String()})
	assertOmitted(t, res, map[string]Reason{fresh.String(): ReasonAbsent})
}

// TestTwoForEachKeysDoNotShareOrShadowARecordKey is the other half of #1355's
// "where I would look": the estate that lost an instance had for_each keys
// "a.b" and "plain", and the one with the dot in it is the one that WAS
// destroyed. A key that is a string prefix of another's would be a real way
// for one record to hide another, since the record namespace is read with a
// prefix LIST.
//
// It is not one. The address is base64url-encoded into the key, so the two
// encodings diverge and neither is a prefix of the other, and both are under
// the same type segment rather than under one another. Pinned by value rather
// than argued, because the encoding is what the argument rests on.
func TestTwoForEachKeysDoNotShareOrShadowARecordKey(t *testing.T) {
	const prefix = "tofu-records/tagged-estate/"
	dotted := RecordKey(prefix, mustAddr(t, `terraform_data.effect["a.b"]`))
	plain := RecordKey(prefix, mustAddr(t, `terraform_data.effect["plain"]`))

	if dotted == plain {
		t.Fatalf("both for_each keys encode to one record key %q", dotted)
	}
	if strings.HasPrefix(dotted, plain) || strings.HasPrefix(plain, dotted) {
		t.Errorf("one record key is a string prefix of the other, so a prefix LIST of one namespace reaches into the other's: %q and %q", dotted, plain)
	}
	for _, key := range []string{dotted, plain} {
		back, ok := RecordAddr(prefix, key)
		if !ok {
			t.Errorf("%q does not decode back to an address, so orphan discovery cannot see it", key)
			continue
		}
		if !strings.HasPrefix(key, prefix+"terraform_data/") {
			t.Errorf("%q is not under the type segment of its own namespace", key)
		}
		if got := back.String(); got != `terraform_data.effect["a.b"]` && got != `terraform_data.effect["plain"]` {
			t.Errorf("%q decodes to %s, which is neither of the two addresses", key, got)
		}
	}
}

// writeTwoNullResourceFixture is writeNullResourceFixture's two-instance
// shape: one resource block, for_each over two keys, which is the estate
// #1355 was measured on (a `terraform_data` with for_each over two strings).
func writeTwoNullResourceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
resource "null_resource" "trigger" {
  for_each = toset(["kept", "missed"])
  triggers = {
    input = each.key
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// seedRecordObject writes addr's kind=object record into store, the shape an
// apply of a record-backed null_resource leaves behind.
func seedRecordObject(t *testing.T, store staterecord.Store, prefix string, addr addrs.AbsResourceInstance) {
	t.Helper()
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("id-" + addr.String()),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	payload, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding the fixture payload for %s: %s", addr, err)
	}
	key := RecordKey(prefix, addr)
	if _, err := store.PutIfAbsent(context.Background(), key, payload); err != nil {
		t.Fatalf("seeding %s: %s", key, err)
	}
}
