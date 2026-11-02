// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
	"testing"
)

// proseOnly answers [ContractChecker] with one store's words and refuses to
// be asked for findings. [ContractRefusalText] renders findings it is given
// and never reads the store, which is what makes this enough.
type proseOnly struct {
	Store
	subject string
	refusal func(namespace string, f Finding) (string, string)
	closing func(refused []Setting) string
}

func (p proseOnly) CheckContract(context.Context, ContractOptions) ([]Finding, error) {
	panic("ContractRefusalText read the store")
}
func (p proseOnly) ContractSubject() (string, string)          { return "Subject", p.subject }
func (p proseOnly) ContractCheckFailed(error) (string, string) { return "", "" }
func (p proseOnly) ContractRefusal(f Finding) (string, string) { return p.refusal(p.subject, f) }
func (p proseOnly) ContractRefusalClosing(refused []Setting) string {
	if p.closing == nil {
		return ""
	}
	return p.closing(refused)
}

// TestContractRefusalTextClosesWithOneWaiverLine is why the closing line
// exists at all. A plain kind cluster refuses an estate's first contact
// twice at once, and each refusal carries its own allow_insecure line; a
// reader who followed both would write the argument twice in one block,
// which does not parse. The store that has no such hazard adds nothing, and
// neither store adds a closing line to a single refusal.
func TestContractRefusalTextClosesWithOneWaiverLine(t *testing.T) {
	cluster := proseOnly{subject: "tofu-records-prod", refusal: ClusterContractRefusal, closing: ClusterContractRefusalClosing}
	bucket := proseOnly{subject: "the-bucket", refusal: BucketContractRefusal}

	two := []Finding{
		{Setting: ClusterEncryptionAtRest, Found: "no encryption provider config"},
		{Setting: ClusterEstateBoundary, Found: "no policy is installed"},
	}
	got := ContractRefusalText(cluster, two)
	for _, want := range []string{"encryption_at_rest", "estate_boundary",
		"To accept all 2 on purpose, the waiver is one line and not 2: `allow_insecure = [\"encryption_at_rest\", \"estate_boundary\"]`"} {
		if !strings.Contains(got, want) {
			t.Errorf("two refusals do not say %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "`") {
		t.Errorf("the pasteable line is not the last thing the reader sees:\n%s", got)
	}

	if one := ContractRefusalText(cluster, two[:1]); strings.Contains(one, "To accept all") {
		t.Errorf("one refusal was given a combined waiver line:\n%s", one)
	}
	if none := ContractRefusalText(cluster, []Finding{{Setting: ClusterEstateBoundary, Outcome: Passed}}); none != "" {
		t.Errorf("a passing finding produced a message: %q", none)
	}

	// The bucket refuses twice with no closing line: its refusals do not
	// each carry an allow_insecure line, so there is no duplicate argument
	// to replace.
	bucketTwo := []Finding{
		{Setting: BucketVersioning, Found: "versioning is Suspended"},
		{Setting: BucketPublicAccessBlock, Found: "public-access block has BlockPublicAcls off"},
	}
	got = ContractRefusalText(bucket, bucketTwo)
	if !strings.Contains(got, "versioning") || !strings.Contains(got, "public_access_block") {
		t.Errorf("the bucket's two refusals are not both there:\n%s", got)
	}
	if strings.Contains(got, "To accept all") {
		t.Errorf("the bucket grew a closing line it has no use for:\n%s", got)
	}
}
