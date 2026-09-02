// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// scanFixture loads a testdata module and collects its naming signal.
func scanFixture(t *testing.T, name string) *ConfigSignal {
	t.Helper()

	cfg := loadConfig(t, filepath.Join("testdata", name), nil)
	signal, diags := ScanConfig(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("scanning %s: %s", name, diags.Err())
	}
	return signal
}

// TestScanConfigReadsEveryInstance pins what the signal reads off each block
// of the mixed fixture: which arguments the instance sets, per instance, for
// a configuration that answers every way there is.
func TestScanConfigReadsEveryInstance(t *testing.T) {
	signal := scanFixture(t, "naming-signal")

	// Set/unset per instance, over the arguments a provider's identity
	// schema would ask about for each type.
	tests := []struct {
		addr  string
		args  []string
		want  Naming
		set   []string
		unset []string
	}{
		// Set: the bucket names itself.
		{addr: "aws_s3_bucket.named", args: []string{"bucket"}, want: NamingClientNamed, set: []string{"bucket"}},
		// Unset: bucket_prefix is not the identity argument, and writing it
		// is not writing a name.
		{addr: "aws_s3_bucket.prefixed", args: []string{"bucket"}, want: NamingServerAssigned, unset: []string{"bucket"}},
		// Written and null, which is the same answer as absent. This is the
		// case presence alone gets wrong.
		{addr: "aws_s3_bucket.nulled", args: []string{"bucket"}, want: NamingServerAssigned, unset: []string{"bucket"}},
		// The other half of the pair the schemas cannot separate.
		{addr: "aws_vpc.main", args: []string{"id"}, want: NamingServerAssigned, unset: []string{"id"}},
		// Partially set: one of two identity arguments.
		{
			addr:  "aws_lb_target_group_attachment.half",
			args:  []string{"target_group_arn", "target_id"},
			want:  NamingPartial,
			set:   []string{"target_group_arn"},
			unset: []string{"target_id"},
		},
		// Per instance out of one body.
		{addr: "aws_cloudwatch_log_group.split[0]", args: []string{"name"}, want: NamingClientNamed, set: []string{"name"}},
		{addr: "aws_cloudwatch_log_group.split[1]", args: []string{"name"}, want: NamingServerAssigned, unset: []string{"name"}},
	}

	byAddr := map[string]string{}
	for _, typeName := range signal.Types() {
		for _, addr := range signal.Instances(typeName) {
			byAddr[addr.String()] = typeName
		}
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if _, ok := byAddr[tc.addr]; !ok {
				t.Fatalf("%s is not in the signal at all; it holds %v", tc.addr, byAddr)
			}
			var found bool
			var got InstanceNaming
			for _, addr := range signal.Instances(byAddr[tc.addr]) {
				if addr.String() == tc.addr {
					got, found = signal.NamingOf(addr, tc.args), true
				}
			}
			if !found {
				t.Fatalf("%s is not among its type's instances", tc.addr)
			}
			if got.Naming != tc.want {
				t.Errorf("naming is %q, want %q (set %v, unset %v)", got.Naming, tc.want, got.Set, got.Unset)
			}
			if !reflect.DeepEqual(got.Set, tc.set) {
				t.Errorf("set arguments are %v, want %v", got.Set, tc.set)
			}
			if !reflect.DeepEqual(got.Unset, tc.unset) {
				t.Errorf("unset arguments are %v, want %v", got.Unset, tc.unset)
			}
		})
	}
}

// TestNamingOfTypeIsUnanimousOrPartial pins the type-level verdict: a type
// whose instances disagree is reported as disagreeing, never rounded to
// whichever answer the first block gave.
func TestNamingOfTypeIsUnanimousOrPartial(t *testing.T) {
	mixed := scanFixture(t, "naming-signal")
	named := scanFixture(t, "naming-signal-named")

	tests := []struct {
		name   string
		signal *ConfigSignal
		typ    string
		args   []string
		want   Naming
		insts  int
	}{
		// One block names itself, two do not.
		{name: "disagreeing buckets", signal: mixed, typ: "aws_s3_bucket", args: []string{"bucket"}, want: NamingPartial, insts: 3},
		// No block names a VPC, and none ever will.
		{name: "vpcs", signal: mixed, typ: "aws_vpc", args: []string{"id"}, want: NamingServerAssigned, insts: 1},
		// One block, half an identity.
		{
			name: "half an attachment", signal: mixed, typ: "aws_lb_target_group_attachment",
			args: []string{"target_group_arn", "target_id"}, want: NamingPartial, insts: 1,
		},
		// Unanimous across a for_each expansion and a plain block.
		{name: "every bucket named", signal: named, typ: "aws_s3_bucket", args: []string{"bucket"}, want: NamingClientNamed, insts: 3},
		// A type the configuration does not declare has no verdict, not a
		// negative one.
		{name: "absent type", signal: named, typ: "aws_iam_role", args: []string{"name"}, want: NamingUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, insts := tc.signal.NamingOfType(tc.typ, tc.args)
			if got != tc.want {
				t.Errorf("verdict is %q, want %q (%v)", got, tc.want, insts)
			}
			if len(insts) != tc.insts {
				t.Errorf("reported %d instances, want %d: %v", len(insts), tc.insts, insts)
			}
		})
	}
}

// A nil signal is the no-configuration case, and it answers "nothing to
// say" rather than panicking or asserting a negative.
func TestNilSignalSaysNothing(t *testing.T) {
	var signal *ConfigSignal

	if got := signal.Len(); got != 0 {
		t.Errorf("a nil signal covers %d instances", got)
	}
	if got := signal.Types(); got != nil {
		t.Errorf("a nil signal names types: %v", got)
	}
	if got, insts := signal.NamingOfType("aws_s3_bucket", []string{"bucket"}); got != NamingUnknown || insts != nil {
		t.Errorf("a nil signal has a verdict: %q %v", got, insts)
	}
}

// Asking about no arguments is not the same as asking about arguments
// nothing sets: an empty question gets no answer.
func TestNamingOfNoArguments(t *testing.T) {
	signal := scanFixture(t, "naming-signal-named")
	got, _ := signal.NamingOfType("aws_s3_bucket", nil)
	if got != NamingUnknown {
		t.Errorf("verdict over no arguments is %q, want %q", got, NamingUnknown)
	}
}

// The signal covers types the admission table has never heard of, which is
// the whole point: those are the types it exists to say something about.
func TestScanConfigCoversUnadmittedTypes(t *testing.T) {
	signal := scanFixture(t, "naming-signal-named")

	if _, inTable := LookupType("aws_fake_queue"); inTable {
		t.Fatal("this test needs a type the table does not cover; aws_fake_queue is a synthetic stand-in for that and should never be added to the hand table")
	}
	insts := signal.Instances("aws_fake_queue")
	if len(insts) != 1 {
		t.Fatalf("the queue is not in the signal: %v", signal.Types())
	}
	if !signal.Sets(insts[0], "name") {
		t.Error("the queue names itself and the signal did not see it")
	}
}

// Resolve collects the signal on its own walk, so a caller that has
// resolved a configuration does not have to scan it again.
func TestResolveAttachesTheSignal(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	signal := result.Signal()
	if signal.Len() != result.Len() {
		t.Errorf("the signal covers %d instances and the resolution %d", signal.Len(), result.Len())
	}
	for _, res := range result.All() {
		if _, ok := signal.set[res.Addr.String()]; !ok {
			t.Errorf("%s was resolved but is absent from the signal", res.Addr)
		}
	}

	// The estate's bucket names itself, and its VPC does not. That is the
	// pair the provider's schemas report identically.
	for _, addr := range signal.Instances("aws_s3_bucket") {
		if !signal.Sets(addr, "bucket") {
			t.Errorf("%s does not set bucket, and the estate's buckets are all named", addr)
		}
	}
	for _, addr := range signal.Instances("aws_vpc") {
		if signal.Sets(addr, "id") {
			t.Errorf("%s sets id, which no VPC block does", addr)
		}
	}
}

// A result nobody resolved - one assembled by marker discovery, say -
// carries no signal, and asking is not an error.
func TestResultWithoutASignal(t *testing.T) {
	res := newResult()
	if res.Signal().Len() != 0 {
		t.Error("a hand-built result should carry no signal")
	}
}

// ScanConfig needs a configuration loaded the way the rest of the package
// needs one, and says so rather than reading half of it.
func TestScanConfigWithoutAnEvaluator(t *testing.T) {
	_, diags := ScanConfig(context.Background(), nil)
	if !diags.HasErrors() {
		t.Fatal("scanning nothing should be an error")
	}
	if !strings.Contains(diags.Err().Error(), "static evaluator") {
		t.Errorf("the error does not name what is missing: %s", diags.Err())
	}
}
