// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// cohortSchemas is the pair the whole config-signal rule exists for, in the
// shapes the real AWS provider serves: aws_s3_bucket's bucket and aws_vpc's
// id are both Optional+Computed arguments that the identity schema requires
// for import, and they mean opposite things. aws_fake_queue is a synthetic
// stand-in for the same shape, outside the hand table on purpose - it used
// to be aws_sqs_queue itself, until the messaging batch (#40, #44) put the
// real type in the table and left this fixture needing a type that stays
// out of it.
func cohortSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_s3_bucket": {
			args:     map[string]string{"bucket": "optcomp", "bucket_prefix": "opt", "id": "optcomp"},
			identity: map[string]string{"bucket": "req", "account_id": "opt", "region": "opt"},
		},
		"aws_fake_queue": {
			args:     map[string]string{"name": "optcomp", "name_prefix": "opt", "id": "optcomp"},
			identity: map[string]string{"name": "req", "account_id": "opt", "region": "opt"},
		},
		"aws_vpc": serverAssignedLike("cidr_block"),
		// Settled by the strict rule with no configuration in the picture,
		// so the config signal must not claim it a second time.
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "value": "opt", "id": "optcomp"},
			identity: map[string]string{"name": "req", "account_id": "opt"},
		},
		// Required for import and computed-only: no configuration can name
		// it, whatever a configuration says.
		"aws_odd_thing": {
			args:     map[string]string{"serial": "comp", "id": "optcomp"},
			identity: map[string]string{"serial": "req"},
		},
	})
}

func derivableByType(ds []DerivableType) map[string]DerivableType {
	out := map[string]DerivableType{}
	for _, d := range ds {
		out[d.Type] = d
	}
	return out
}

// TestDerivableWithConfigSignal is the point of the whole file: the cohort
// the schemas cannot decide, decided by a configuration that names its
// resources.
func TestDerivableWithConfigSignal(t *testing.T) {
	schemas := cohortSchemas()
	signal := scanFixture(t, "naming-signal-named")

	got := derivableByType(DerivableWith(schemas, signal))

	bucket, ok := got["aws_s3_bucket"]
	if !ok {
		t.Fatalf("the configuration names every bucket and aws_s3_bucket was still not admitted; admitted: %v", got)
	}
	if bucket.Admits != AdmitConfigSignal {
		t.Errorf("aws_s3_bucket was admitted as %q, want %q", bucket.Admits, AdmitConfigSignal)
	}
	if !reflect.DeepEqual(bucket.IdentityAttrs, []string{"bucket"}) {
		t.Errorf("identity attributes are %v, want [bucket]", bucket.IdentityAttrs)
	}
	if !reflect.DeepEqual(bucket.Context, []string{"account_id", "region"}) {
		t.Errorf("context attributes are %v, want [account_id region]", bucket.Context)
	}
	if !bucket.InTable {
		t.Error("aws_s3_bucket is in the hand table and should be marked so")
	}
	// The evidence travels with the verdict: three blocks, each naming
	// itself.
	if len(bucket.Naming) != 3 {
		t.Fatalf("the verdict carries %d instances of evidence, want 3: %v", len(bucket.Naming), bucket.Naming)
	}
	for _, one := range bucket.Naming {
		if one.Naming != NamingClientNamed || !reflect.DeepEqual(one.Set, []string{"bucket"}) {
			t.Errorf("%s: %q set %v", one.Addr, one.Naming, one.Set)
		}
	}

	queue, ok := got["aws_fake_queue"]
	if !ok {
		t.Error("the queue names itself and was not admitted")
	} else if queue.InTable {
		t.Error("aws_fake_queue is not in the hand table")
	}

	if d, ok := got["aws_vpc"]; ok {
		t.Errorf("no block names a VPC and aws_vpc was admitted anyway as %q", d.Admits)
	}
	if d, ok := got["aws_odd_thing"]; ok {
		t.Errorf("a computed-only identity attribute was admitted as %q", d.Admits)
	}

	// The strict rule's own verdicts survive unchanged and keep their own
	// evidence.
	param, ok := got["aws_ssm_parameter"]
	if !ok {
		t.Fatal("the strict rule's admissions must survive the upgrade")
	}
	if param.Admits != AdmitSchema {
		t.Errorf("aws_ssm_parameter was admitted as %q, want %q", param.Admits, AdmitSchema)
	}
	if len(param.Naming) != 0 {
		t.Errorf("a schema-settled verdict carries per-instance evidence: %v", param.Naming)
	}

	// And nothing is reported twice.
	seen := map[string]int{}
	for _, d := range DerivableWith(schemas, signal) {
		seen[d.Type]++
	}
	for typeName, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in the derivable set", typeName, n)
		}
	}
}

// A configuration that declines - the prefixed bucket, the null one, the
// VPC - admits nothing extra. The signal is not a way to say yes; it is a
// way to read an answer.
func TestDerivableWithDecliningConfig(t *testing.T) {
	schemas := cohortSchemas()
	signal := scanFixture(t, "naming-signal")

	got := derivableByType(DerivableWith(schemas, signal))

	// Three buckets, one named: unanimity is the bar, so this is a no.
	if d, ok := got["aws_s3_bucket"]; ok {
		t.Errorf("one named bucket out of three admitted the type as %q", d.Admits)
	}
	if _, ok := got["aws_vpc"]; ok {
		t.Error("aws_vpc admitted itself")
	}
	if _, ok := got["aws_ssm_parameter"]; !ok {
		t.Error("the strict rule's admissions are not a configuration's to withdraw")
	}
}

// With no configuration, this is exactly Derivable. The nil case is the
// survey generator's, which reads a provider release and no estate.
func TestDerivableWithNilSignal(t *testing.T) {
	schemas := cohortSchemas()

	strict := Derivable(schemas)
	withNil := DerivableWith(schemas, nil)
	if !reflect.DeepEqual(strict, withNil) {
		t.Errorf("a nil signal changed the derivable set:\n strict: %v\n with nil: %v", strict, withNil)
	}
}

// DerivableNewWith is the batch a wiring lane would not have to write.
func TestDerivableNewWith(t *testing.T) {
	schemas := cohortSchemas()
	signal := scanFixture(t, "naming-signal-named")

	var got []string
	for _, d := range DerivableNewWith(schemas, signal) {
		got = append(got, d.Type)
	}
	// aws_s3_bucket and aws_ssm_parameter are already in the table; the
	// queue is not.
	if !reflect.DeepEqual(got, []string{"aws_fake_queue"}) {
		t.Errorf("the new-candidate set is %v, want [aws_fake_queue]", got)
	}
}

// The cohort is the types a configuration could decide, and nothing else.
func TestCohortAttrs(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		// In: one settable identity attribute.
		"one_optional": {
			args:     map[string]string{"name": "optcomp"},
			identity: map[string]string{"name": "req"},
		},
		// In: a required one and a settable one. The required half is
		// already settled and the settable half is what a configuration
		// answers, so the row asks for both.
		"mixed": {
			args:     map[string]string{"cluster": "req", "name": "optcomp"},
			identity: map[string]string{"cluster": "req", "name": "req"},
		},
		// Out: the strict rule settles it, so the weaker evidence must not
		// stand in for the stronger.
		"all_required": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req"},
		},
		// Out: configuration cannot set it.
		"computed": {
			args:     map[string]string{"serial": "comp"},
			identity: map[string]string{"serial": "req"},
		},
		// Out: no argument by that name at all.
		"absent": {
			args:     map[string]string{"id": "optcomp"},
			identity: map[string]string{"serial": "req"},
		},
		// Out: nothing is required for import, so nothing says what names it.
		"nothing_required": {
			args:     map[string]string{"enabled": "opt"},
			identity: map[string]string{"account_id": "opt"},
		},
		// Out: no identity schema at all.
		"no_identity": {args: map[string]string{"name": "optcomp"}},
	})

	want := map[string][]string{
		"one_optional": {"name"},
		"mixed":        {"cluster", "name"},
	}
	for typeName, schema := range schemas {
		attrs, ok := cohortAttrs(schema)
		expect, inCohort := want[typeName]
		if ok != inCohort {
			t.Errorf("%s: in the cohort = %v, want %v (%v)", typeName, ok, inCohort, attrs)
			continue
		}
		if ok && !reflect.DeepEqual(attrs, expect) {
			t.Errorf("%s: cohort attributes are %v, want %v", typeName, attrs, expect)
		}
	}
}

// The verification reports the config-settled admissions alongside the
// schema-settled ones, which is how the projection seam gets at them.
func TestVerifyTableInCarriesConfigAdmissions(t *testing.T) {
	schemas := cohortSchemas()
	signal := scanFixture(t, "naming-signal-named")

	v := VerifyTableIn(schemas, signal)

	byEvidence := map[Admission][]string{}
	for _, d := range v.Derivable {
		byEvidence[d.Admits] = append(byEvidence[d.Admits], d.Type)
	}
	if !reflect.DeepEqual(byEvidence[AdmitSchema], []string{"aws_ssm_parameter"}) {
		t.Errorf("schema-settled admissions are %v", byEvidence[AdmitSchema])
	}
	if !reflect.DeepEqual(byEvidence[AdmitConfigSignal], []string{"aws_fake_queue", "aws_s3_bucket"}) {
		t.Errorf("config-settled admissions are %v", byEvidence[AdmitConfigSignal])
	}

	// And the summary says which is which, because the two are different
	// claims.
	if s := v.Summary(); !strings.Contains(s, "only because this configuration names them") {
		t.Errorf("the summary does not separate the two kinds of evidence:\n%s", s)
	}

	// VerifyTable is the same check with nothing to read.
	plain := VerifyTable(schemas)
	for _, d := range plain.Derivable {
		if d.Admits != AdmitSchema {
			t.Errorf("%s was admitted as %q with no configuration", d.Type, d.Admits)
		}
	}
}
