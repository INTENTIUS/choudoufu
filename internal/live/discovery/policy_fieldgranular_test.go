// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/policy"
)

// fieldGranularOrphanResult is what the field-manager sweep files for a
// kubernetes_annotations block that left the configuration: no labels of
// its own (Tags nil), its marker the estate's field manager.
func fieldGranularOrphanResult() *Result {
	return &Result{Orphans: []OwnedResource{{
		TypeName:           "kubernetes_annotations",
		ImportID:           "apiVersion=storage.k8s.io/v1,kind=StorageClass,name=standard",
		Marker:             estateName,
		Normalized:         "kubernetes_annotations.orphan_storageclass_standard",
		Swept:              true,
		Removal:            true,
		FieldManagerMarked: true,
	}}}
}

// TestFieldGranularOrphanIsUndeclaredTagged is corpus-govuk-cluster-
// services' day2_remove (#1885): a field-granular block removed from the
// configuration was withheld as undeclared_untagged = "keep" under the
// default policy, because the orphan has no labels to match the policy's
// tag against. Its marker is the estate's field manager, which is the
// estate marker for this shape ([markers.FieldManagerFor]), so under the
// default tag_key it is undeclared_tagged, whose default is delete, and
// the removal stands - #1863's release-on-removal path.
func TestFieldGranularOrphanIsUndeclaredTagged(t *testing.T) {
	res := fieldGranularOrphanResult()
	applyOrphanPolicy(Request{Estate: estateName, Policy: policy.Build(nil, estateName)}, res)
	o := res.Orphans[0]
	if !o.Removal {
		t.Fatalf("the default policy withheld a field-granular orphan the estate's manager owns: %s", o.Withheld)
	}

	res = fieldGranularOrphanResult()
	applyOrphanPolicy(Request{Estate: estateName, Policy: policy.Build(&policy.Raw{UndeclaredTagged: "keep", UndeclaredTaggedSet: true}, estateName)}, res)
	o = res.Orphans[0]
	if o.Removal || o.PolicyQuadrant != "undeclared_tagged" || !strings.Contains(o.Withheld, "policy.undeclared_tagged") {
		t.Errorf("undeclared_tagged = \"keep\" did not govern the orphan: removal=%v quadrant=%q withheld=%q", o.Removal, o.PolicyQuadrant, o.Withheld)
	}
}

// TestFieldGranularOrphanCustomTagKeyIsUntagged: a policy keyed on a tag
// other than the estate marker reads no field-granular write as carrying
// it - the write has no tags of its own - so the untagged quadrant
// applies, and the quadrant recorded is the one whose verb was applied.
func TestFieldGranularOrphanCustomTagKeyIsUntagged(t *testing.T) {
	res := fieldGranularOrphanResult()
	applyOrphanPolicy(Request{Estate: estateName, Policy: policy.Build(&policy.Raw{
		TagKey: "team-owns", TagKeySet: true,
		TagValue: "platform", TagValueSet: true,
	}, estateName)}, res)
	o := res.Orphans[0]
	if o.Removal || o.PolicyQuadrant != "undeclared_untagged" || !strings.Contains(o.Withheld, "policy.undeclared_untagged") {
		t.Errorf("removal=%v quadrant=%q withheld=%q; want withheld under undeclared_untagged", o.Removal, o.PolicyQuadrant, o.Withheld)
	}
}
