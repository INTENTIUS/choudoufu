// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #364's safety half, and the reason it is written here rather
// than left to internal/live/check's identity golden: the golden renders
// nothing for the two classes this change admits. A RECORD_BACKED or
// RECORD_LOCATED instance has no plan-time identity string to pin - the id
// is server-minted and read back after the apply - so those rows are blank
// in identity-golden.txt by construction and the golden cannot tell whether
// this change moved them. It has to be asserted here, by value.
//
// HANDOFF.md's safety rule: "convergence is never evidence an identity is
// right: assert the rendered identity by value." What is knowable by value
// before an apply for these two classes is WHERE the identity will be
// filed - the store key - and that is precisely the thing whose being
// wrong is unrecoverable. A record filed under the wrong key is read back
// by the wrong instance on the next run, and the run then imports one
// resource's live object into another resource's slot: the record-rung
// version of writing a wrong marker. The keys below are hand-written
// literals, not calls to the functions that produce them, so that a change
// to the key grammar shows up as this test failing rather than as this
// test agreeing with itself.

const impliedStoreEstate = "implied-store-estate"

// impliedStoreLocatableType is a real type from identity.MarkerlessTypes
// that a clean schema admits as record-located. Naming it is what lets the
// keys below be literals; the subtest guards against it silently ceasing
// to qualify, in which case pick another from the same set rather than
// deleting the assertion.
const impliedStoreLocatableType = "aws_acmpca_certificate"

// impliedStoreConfig is the whole point of issue #364 written as HCL: a
// stock-shaped configuration with a live block added and nothing else.
//
// It carries one instance of each thing the implied store's existence
// changes the verdict on, plus one it must NOT change - aws_s3_bucket,
// which is taggable, marker-bearing and was admitted all along. That last
// one is the control: if implying a store moved an ordinary marker-bearing
// identity, this test says so by value.
func impliedStoreConfig(t *testing.T, live string) *configs.Config {
	t.Helper()
	src := live + `
resource "aws_s3_bucket" "marked" {
  bucket = "implied-store-bucket"
}

resource "null_resource" "effect" {
}

resource "` + impliedStoreLocatableType + `" "thing" {
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return loadConfigDir(t, dir)
}

// impliedStoreSchemas is the schema map the resolution runs against: a
// clean string id for the locatable type (which is what identity.LocatedType
// reads) and a bucket/tags shape for the control resource.
func impliedStoreSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		impliedStoreLocatableType: {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		}},
		"aws_s3_bucket": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":     {Type: cty.String, Computed: true},
				"bucket": {Type: cty.String, Optional: true, Computed: true},
				"tags":   {Type: cty.Map(cty.String), Optional: true},
			},
		}},
		"null_resource": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		}},
	}
}

const impliedStoreLiveBlock = `
terraform {
  live {
    estate = "` + impliedStoreEstate + `"
  }
}
`

const impliedStoreDeclaredLiveBlock = `
terraform {
  live {
    estate = "` + impliedStoreEstate + `"
    record_store "local" {}
  }
}
`

// TestImpliedRecordStoreFilesRecordsUnderTheExactKeys is the by-value
// assertion. Every string below is written out; nothing is compared against
// the function that produced it.
func TestImpliedRecordStoreFilesRecordsUnderTheExactKeys(t *testing.T) {
	schemas := impliedStoreSchemas()

	if !identity.LocatedType(impliedStoreLocatableType, schemas) {
		t.Fatalf("%s is no longer admitted as record-located with a clean schema, so this test measures nothing. Pick another name from identity.MarkerlessTypes that still qualifies (internal/live/lint's aLocatableType picks one for you) and update the literal keys below to match.", impliedStoreLocatableType)
	}

	cfg := impliedStoreConfig(t, impliedStoreLiveBlock)

	// Nothing may be refused. This is the estate shape HANDOFF.md promises
	// works out of the box, and before #364 it refused twice.
	for _, issue := range CheckWith(t.Context(), cfg, Context{Schemas: schemas}) {
		t.Errorf("a configuration with a live block and nothing else was refused: %s: %s\n%s", issue.Rule, issue.Construct, issue.Detail)
	}

	result, diags := identity.ResolveWith(t.Context(), cfg, identity.Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolution refused: %s", diags.Err())
	}

	byAddr := map[string]identity.Resolution{}
	for _, res := range result.All() {
		byAddr[res.Addr.String()] = res
	}

	// The control. aws_s3_bucket was admitted before this change and after
	// it, and its rendered identity is the string a marker will be stamped
	// against. Implying a record store must not have moved it by one byte.
	bucket, ok := byAddr["aws_s3_bucket.marked"]
	if !ok {
		t.Fatalf("aws_s3_bucket.marked did not resolve at all; addresses present: %v", addrKeys(byAddr))
	}
	if got, want := string(bucket.Class), "CONCRETE"; got != want {
		t.Errorf("aws_s3_bucket.marked class = %q, want %q", got, want)
	}
	if got, want := bucket.ImportID, "implied-store-bucket"; got != want {
		t.Errorf("aws_s3_bucket.marked identity = %q, want %q. The implied record store moved a marker-bearing identity, which is the wrong-marker failure this test exists to catch.", got, want)
	}

	// The record-backed logical type: admitted by the implied store, and
	// its record filed here and nowhere else.
	effect, ok := byAddr["null_resource.effect"]
	if !ok {
		t.Fatalf("null_resource.effect did not resolve; the implied store did not admit it. Addresses present: %v", addrKeys(byAddr))
	}
	if got, want := string(effect.Class), "RECORD_BACKED"; got != want {
		t.Errorf("null_resource.effect class = %q, want %q", got, want)
	}
	if got, want := projection.RecordKey(projection.RecordKeyPrefix(impliedStoreEstate), mustParseInstance(t, "null_resource.effect")),
		"tofu-records/implied-store-estate/null_resource/bnVsbF9yZXNvdXJjZS5lZmZlY3Q"; got != want {
		t.Errorf("null_resource.effect's record key = %q, want %q", got, want)
	}

	// The record-located type: admitted by the implied store, and its
	// identity filed at the SAME key a record-backed instance's object
	// would use - GitHub issue #364 folded the located namespace into the
	// one record envelope, distinguished by the envelope's "kind" rather
	// than by which root a key lives under. Kind is what now keeps orphan
	// discovery from treating a lost or stale identity as delete authority
	// for the object it names (recordKindIdentity is never enumerated for
	// destruction the way recordKindObject is).
	thing, ok := byAddr[impliedStoreLocatableType+".thing"]
	if !ok {
		t.Fatalf("%s.thing did not resolve; the implied store did not admit it. Addresses present: %v", impliedStoreLocatableType, addrKeys(byAddr))
	}
	if got, want := string(thing.Class), "RECORD_LOCATED"; got != want {
		t.Errorf("%s.thing class = %q, want %q", impliedStoreLocatableType, got, want)
	}
	if got, want := projection.RecordKey(projection.RecordKeyPrefix(impliedStoreEstate), mustParseInstance(t, impliedStoreLocatableType+".thing")),
		"tofu-records/implied-store-estate/aws_acmpca_certificate/YXdzX2FjbXBjYV9jZXJ0aWZpY2F0ZS50aGluZw"; got != want {
		t.Errorf("%s.thing's record key = %q, want %q", impliedStoreLocatableType, got, want)
	}

	// The implied store's own resolved shape, by value. An empty Path is
	// what internal/live/projection.NewRecordStore turns into a
	// ".tofu-records" directory beside the module; a Type of anything but
	// "local" would send a configuration that named no cloud store at all
	// off to build an AWS client.
	rs := cfg.Module.Live.RecordStore
	if rs == nil {
		t.Fatal("Live.RecordStore is nil for a live block with no record_store block")
	}
	if rs.Type != "local" || rs.Path != "" || !rs.Implied {
		t.Errorf("the implied store is %+v, want Type=local Path=\"\" Implied=true", rs)
	}
}

// TestImpliedRecordStoreMatchesADeclaredOne is the claim "implied" makes,
// stated as an equality rather than as two lists of assertions: the same
// configuration with `record_store "local" {}` written out resolves every
// instance to the same class and the same rendered identity. A difference
// anywhere here means the implied store is a THIRD behavior rather than the
// default one, and every downstream reader would be entitled to a different
// answer depending on which one it got.
func TestImpliedRecordStoreMatchesADeclaredOne(t *testing.T) {
	schemas := impliedStoreSchemas()

	render := func(live string) map[string]string {
		cfg := impliedStoreConfig(t, live)
		result, diags := identity.ResolveWith(t.Context(), cfg, identity.Context{Schemas: schemas})
		if diags.HasErrors() {
			t.Fatalf("resolution refused: %s", diags.Err())
		}
		out := map[string]string{}
		for _, res := range result.All() {
			out[res.Addr.String()] = string(res.Class) + "\t" + res.ImportID
		}
		return out
	}

	implied := render(impliedStoreLiveBlock)
	declared := render(impliedStoreDeclaredLiveBlock)

	if len(implied) == 0 {
		t.Fatal("nothing resolved at all; the comparison below would be vacuous")
	}
	for addr, want := range declared {
		if got := implied[addr]; got != want {
			t.Errorf("%s: implied store resolved %q, declared store resolved %q", addr, got, want)
		}
	}
	for addr := range implied {
		if _, ok := declared[addr]; !ok {
			t.Errorf("%s resolved under the implied store and not under the declared one", addr)
		}
	}
}

func addrKeys(m map[string]identity.Resolution) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustParseInstance(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", s, diags.Err())
	}
	return addr
}
