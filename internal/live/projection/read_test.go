// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #187's read half. [ReadInstances] is what populates
// [identity.Context.ManagedResults], the seam a prior slot landed inert: a
// reference to a COMPUTED attribute of a sibling managed resource -
// aws_acm_certificate.cert.domain_validation_options - can be resolved only
// against what the cloud holds, and this is where that value comes from.
//
// Every assertion here is on the VALUE returned, not on a predicate about
// it, because the value is what a second resolution pass turns into an
// ownership marker.

const readEstate = "read-unit"

// readOwned is the fake cloud both halves of the expanded block live in,
// tagged for this estate so the ownership rule admits them.
func readOwned(c *fakeCloud, importID string) {
	c.putTagged("aws_cloudwatch_log_group", importID, map[string]string{
		"id": importID, "name": importID, "arn": "arn:aws:logs:::" + importID,
	}, map[string]string{markers.TagEstate: readEstate})
}

func expandedResolutions(t *testing.T) []identity.Resolution {
	t.Helper()
	return []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app["a"]`), Class: identity.ClassConcrete, ImportID: "/ours/a"},
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app["b"]`), Class: identity.ClassConcrete, ImportID: "/ours/b"},
	}
}

func readKeys(v *ReadValues) []string {
	out := make([]string, 0, len(v.Values))
	for k := range v.Values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unreadReasons(v *ReadValues) map[string]Reason {
	out := make(map[string]Reason, len(v.Unread))
	for _, o := range v.Unread {
		out[o.Addr.String()] = o.Reason
	}
	return out
}

// TestReadInstancesReturnsTheWholeLiveObject is the base claim, asserted on
// the attribute values rather than on presence: what comes back is keyed the
// way [identity.Context.ManagedResults] keys its input, and each value is the
// live object the provider served, so a reference to any attribute of it
// resolves.
func TestReadInstancesReturnsTheWholeLiveObject(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a")
	readOwned(cloud, "/ours/b")

	got, diags := ReadInstances(context.Background(), cfg, expandedResolutions(t), cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}})
	assertNoErrors(t, diags)

	// The instance count is asserted separately from the key set on purpose:
	// a map with the right keys and a different number of entries cannot
	// happen, but the same claim over a list has been wrong here before.
	if len(got.Values) != 2 {
		t.Fatalf("read %d instances, want 2: %v", len(got.Values), readKeys(got))
	}
	wantKeys := []string{`aws_cloudwatch_log_group.app["a"]`, `aws_cloudwatch_log_group.app["b"]`}
	if k := readKeys(got); k[0] != wantKeys[0] || k[1] != wantKeys[1] {
		t.Fatalf("read keys %v, want %v", k, wantKeys)
	}

	for key, wantName := range map[string]string{
		`aws_cloudwatch_log_group.app["a"]`: "/ours/a",
		`aws_cloudwatch_log_group.app["b"]`: "/ours/b",
	} {
		val := got.Values[key]
		if val.IsNull() || !val.Type().IsObjectType() {
			t.Fatalf("%s came back as %#v, want the live object", key, val)
		}
		if name := val.GetAttr("name"); name.AsString() != wantName {
			t.Errorf("%s.name is %q, want %q", key, name.AsString(), wantName)
		}
		if arn := val.GetAttr("arn"); arn.AsString() != "arn:aws:logs:::"+wantName {
			t.Errorf("%s.arn is %q, want the live arn", key, arn.AsString())
		}
	}
	if len(got.Unread) != 0 {
		t.Errorf("unread %v, want nothing", got.Unread)
	}
}

// TestReadInstancesDropsAPartlyReadBlock is the one that matters most, and
// it is about a wrong answer rather than a missing one. A for_each block's
// value is an object over all its instances; assembled from one of two, it
// would expand a dependent for_each to one key and the second live resource
// would be planned as a create beside an object nobody named.
func TestReadInstancesDropsAPartlyReadBlock(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a") // and nothing at /ours/b

	got, diags := ReadInstances(context.Background(), cfg, expandedResolutions(t), cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}})
	assertNoErrors(t, diags)

	if len(got.Values) != 0 {
		t.Fatalf("read %v, want nothing: one instance of the block was unreadable, so the block has no aggregate", readKeys(got))
	}
	want := map[string]Reason{
		`aws_cloudwatch_log_group.app["a"]`: ReasonIncompleteBlock,
		`aws_cloudwatch_log_group.app["b"]`: ReasonAbsent,
	}
	got.assertUnread(t, want)
}

// The mutation check for the case above: put the missing half back and only
// that, and the block resolves. Without this, the test proves the block is
// dropped, not that it is dropped for the reason claimed.
func TestReadInstancesKeepsTheBlockOnceItIsWhole(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a")
	readOwned(cloud, "/ours/b")

	got, diags := ReadInstances(context.Background(), cfg, expandedResolutions(t), cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}})
	assertNoErrors(t, diags)

	if len(got.Values) != 2 {
		t.Fatalf("read %d instances, want 2: %v", len(got.Values), readKeys(got))
	}
}

// An unowned live object is not a value this phase hands back. Resolving a
// dependent's for_each against the attributes of a resource this estate does
// not own would mint ownership markers for the children of somebody else's
// resource - the same adoption audit finding C1 closed for prior state, on a
// path that did not exist when it was closed.
func TestReadInstancesRefusesAnUnownedObject(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a")
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/b", map[string]string{
		"id": "/ours/b", "name": "/ours/b",
	}, nil) // exists, carries no marker for this estate

	got, diags := ReadInstances(context.Background(), cfg, expandedResolutions(t), cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}})
	assertNoErrors(t, diags)

	if len(got.Values) != 0 {
		t.Fatalf("read %v, want nothing: the block's second instance is somebody else's", readKeys(got))
	}
	got.assertUnread(t, map[string]Reason{
		`aws_cloudwatch_log_group.app["a"]`: ReasonIncompleteBlock,
		`aws_cloudwatch_log_group.app["b"]`: ReasonUnowned,
	})
}

// A resolution this run cannot name is reported, not dropped. The second
// resolution pass will refuse the reference that demanded it exactly as the
// first did, and a caller comparing the two passes has to be able to say
// which of the two happened.
func TestReadInstancesReportsWhatItCannotName(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a")
	readOwned(cloud, "/ours/b")

	res := append(expandedResolutions(t),
		identity.Resolution{
			Addr:   mustAddr(t, `aws_kms_key.root`),
			Class:  identity.ClassNeedsDiscovery,
			Reason: "its identity is assigned by the provider.",
		},
		identity.Resolution{
			Addr:  mustAddr(t, `aws_cloudwatch_log_group.app["c"]`),
			Class: identity.ClassRecordBacked,
		},
	)

	got, diags := ReadInstances(context.Background(), cfg, res, cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}})
	assertNoErrors(t, diags)

	// The record-backed instance is nominally in the same block as the two
	// readable ones, so the incomplete-block rule takes all three out. That
	// is the conservative direction and it is what the assertion pins.
	if len(got.Values) != 0 {
		t.Fatalf("read %v, want nothing", readKeys(got))
	}
	got.assertUnread(t, map[string]Reason{
		`aws_cloudwatch_log_group.app["a"]`: ReasonIncompleteBlock,
		`aws_cloudwatch_log_group.app["b"]`: ReasonIncompleteBlock,
		`aws_cloudwatch_log_group.app["c"]`: ReasonUnreadable,
		`aws_kms_key.root`:                  ReasonNeedsDiscovery,
	})
}

// The narrow read must not do any of the things a full projection does. The
// record store is the one that would be destructive to get wrong: a full
// build lists it to find records whose configuration is gone, and a first
// pass run against an incomplete resolution list is exactly the state in
// which that list is misleading.
func TestReadInstancesNeverOpensTheRecordStore(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")

	cloud := newFakeCloud()
	readOwned(cloud, "/ours/a")
	readOwned(cloud, "/ours/b")

	store := &fatalStore{t: t}
	got, diags := ReadInstances(context.Background(), cfg, expandedResolutions(t), cloud.providers(t),
		Options{Ownership: &Ownership{Estate: readEstate}, RecordStore: store, RecordKeyPrefix: "x/"})
	assertNoErrors(t, diags)

	if len(got.Values) != 2 {
		t.Fatalf("read %d instances, want 2", len(got.Values))
	}
}

// An empty demand costs nothing and reaches no provider, which is what makes
// the phase free for the configurations that do not need it - every one that
// refuses nothing.
func TestReadInstancesWithNoDemandTouchesNothing(t *testing.T) {
	cfg := loadConfig(t, "testdata/expanded")
	cloud := newFakeCloud()

	got, diags := ReadInstances(context.Background(), cfg, nil, cloud.providers(t), Options{})
	assertNoErrors(t, diags)

	if len(got.Values) != 0 || len(got.Unread) != 0 {
		t.Fatalf("an empty demand produced %v / %v", got.Values, got.Unread)
	}
	if len(cloud.imports) != 0 || cloud.reads != 0 {
		t.Fatalf("an empty demand reached the provider: %d imports, %d reads", len(cloud.imports), cloud.reads)
	}
}

func (v *ReadValues) assertUnread(t *testing.T, want map[string]Reason) {
	t.Helper()
	got := unreadReasons(v)
	if len(got) != len(want) {
		t.Fatalf("unread %v, want %v", got, want)
	}
	for addr, reason := range want {
		if got[addr] != reason {
			t.Errorf("%s unread as %q, want %q", addr, got[addr], reason)
		}
	}
	// Address order, so a caller rendering the list gets a stable one.
	for i := 1; i < len(v.Unread); i++ {
		if v.Unread[i-1].Addr.String() > v.Unread[i].Addr.String() {
			t.Fatalf("unread list is not in address order: %v", v.Unread)
		}
	}
}

// fatalStore fails the test on any call. It is how "this phase does not open
// the record store" is proved rather than asserted.
type fatalStore struct{ t *testing.T }

func (s *fatalStore) Get(context.Context, string) ([]byte, string, bool, error) {
	s.t.Fatal("the narrow read opened the record store")
	return nil, "", false, nil
}

func (s *fatalStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	s.t.Fatal("the narrow read wrote to the record store")
	return "", nil
}

func (s *fatalStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	s.t.Fatal("the narrow read wrote to the record store")
	return "", nil
}

func (s *fatalStore) Delete(context.Context, string, string) error {
	s.t.Fatal("the narrow read deleted from the record store")
	return nil
}

func (s *fatalStore) List(context.Context, string) ([]string, error) {
	s.t.Fatal("the narrow read listed the record store")
	return nil, nil
}

var _ staterecord.Store = (*fatalStore)(nil)
