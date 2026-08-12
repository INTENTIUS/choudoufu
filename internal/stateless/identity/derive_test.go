// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"
)

func TestDerivable(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		// Client-named and provable: the provider requires the name to
		// import, and the configuration is required to set it.
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "value": "opt", "id": "optcomp", "region": "optcomp"},
			identity: map[string]string{"name": "req", "region": "opt", "account_id": "opt"},
		},
		// Two required halves, both required arguments.
		"aws_iam_role_policy_attachment": {
			args:     map[string]string{"role": "req", "policy_arn": "req", "id": "optcomp"},
			identity: map[string]string{"role": "req", "policy_arn": "req", "account_id": "opt"},
		},
		// Derivable, and a parent-derived value in practice: route_table_id
		// is a required argument holding a live rtb- ID. Derivability is
		// about which argument supplies the identity, not about whether its
		// value is statically known.
		"aws_route": {
			args:     map[string]string{"route_table_id": "req", "destination_cidr_block": "optcomp", "id": "optcomp"},
			identity: map[string]string{"route_table_id": "req", "destination_cidr_block": "opt", "account_id": "opt"},
		},
		// Not derivable, and the reason this rule is the strict one: the
		// legacy SDK marks bucket Optional+Computed, exactly as it marks
		// aws_vpc's id below. A rule that accepted Optional+Computed would
		// have to accept both.
		"aws_s3_bucket": {
			args:     map[string]string{"bucket": "optcomp", "bucket_prefix": "opt", "id": "optcomp"},
			identity: map[string]string{"bucket": "req", "account_id": "opt"},
		},
		"aws_vpc": serverAssignedLike("cidr_block"),
		// No identity schema at all: nothing to derive from.
		"aws_ecs_cluster": {args: map[string]string{"name": "req", "id": "optcomp"}},
		// An identity schema that requires nothing for import: a
		// singleton account-wide setting. It does not say the
		// configuration names it.
		"aws_ebs_encryption_by_default": {
			args:     map[string]string{"enabled": "opt", "id": "optcomp"},
			identity: map[string]string{"account_id": "opt", "region": "opt"},
		},
		// Required for import, but the type has no argument by that name at
		// all: the provider computes it.
		"aws_odd_thing": {
			args:     map[string]string{"id": "optcomp"},
			identity: map[string]string{"serial": "req"},
		},
	})

	got := make([]string, 0)
	for _, d := range Derivable(schemas) {
		got = append(got, d.Type)
	}
	want := []string{"aws_iam_role_policy_attachment", "aws_route", "aws_ssm_parameter"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("derivable set is %v, want %v", got, want)
	}
}

func TestDerivableFields(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "id": "optcomp"},
			identity: map[string]string{"name": "req", "region": "opt", "account_id": "opt"},
		},
		// Not in the hand table: the shape a wiring batch would pick up.
		"aws_cloudwatch_log_metric_filter": {
			args:     map[string]string{"name": "req", "log_group_name": "req", "id": "optcomp"},
			identity: map[string]string{"name": "req", "log_group_name": "req", "account_id": "opt"},
		},
	})

	all := Derivable(schemas)
	if len(all) != 2 {
		t.Fatalf("derivable set is %v, want two entries", all)
	}

	byType := map[string]DerivableType{}
	for _, d := range all {
		byType[d.Type] = d
	}

	known := byType["aws_ssm_parameter"]
	if !known.InTable {
		t.Error("aws_ssm_parameter is in the hand table and should be marked so")
	}
	if !reflect.DeepEqual(known.IdentityAttrs, []string{"name"}) {
		t.Errorf("identity attributes are %v, want [name]", known.IdentityAttrs)
	}
	if !reflect.DeepEqual(known.Context, []string{"account_id", "region"}) {
		t.Errorf("context attributes are %v, want [account_id region]", known.Context)
	}

	candidate := byType["aws_cloudwatch_log_metric_filter"]
	if candidate.InTable {
		t.Error("aws_cloudwatch_log_metric_filter is not in the hand table")
	}
	if !reflect.DeepEqual(candidate.IdentityAttrs, []string{"log_group_name", "name"}) {
		t.Errorf("identity attributes are %v, want [log_group_name name]", candidate.IdentityAttrs)
	}

	newOnes := DerivableNew(schemas)
	if len(newOnes) != 1 || newOnes[0].Type != "aws_cloudwatch_log_metric_filter" {
		t.Errorf("the new-candidate set is %v, want only aws_cloudwatch_log_metric_filter", newOnes)
	}
}

// The derivable set is reported alongside the verification, which is how a
// wiring lane gets at it.
func TestVerificationCarriesDerivable(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "id": "optcomp"},
			identity: map[string]string{"name": "req", "account_id": "opt"},
		},
		// Deliberately a type the hand table does not cover, so the InTable
		// split has something on both sides. aws_sqs_queue used to stand
		// here and is in the table now (#21's account-derived slice).
		"aws_cloudwatch_log_metric_filter": {
			args:     map[string]string{"name": "req", "log_group_name": "req"},
			identity: map[string]string{"name": "req", "log_group_name": "req", "account_id": "opt"},
		},
	})

	v := VerifyTable(schemas)
	if len(v.Derivable) != 2 {
		t.Fatalf("verification carries %v, want both derivable types", v.Derivable)
	}
	var newly []string
	for _, d := range v.Derivable {
		if !d.InTable {
			newly = append(newly, d.Type)
		}
	}
	if !reflect.DeepEqual(newly, []string{"aws_cloudwatch_log_metric_filter"}) {
		t.Errorf("new candidates are %v, want [aws_cloudwatch_log_metric_filter]", newly)
	}
}
