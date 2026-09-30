// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1743, half (a). #1637 lets an apply create an object that
// carries no marker because a writable record store will record its
// identity afterwards. If the applied object then carries no usable identity
// (the provider left the server-assigned name null, which OpenTofu's apply
// consistency check permits for a Computed attribute that was unknown at
// plan time - see #1675), the write-back cannot record it. The object then
// has neither a marker nor a record, which is exactly what #950 exists to
// prevent, so the run must fail loudly rather than log at INFO and exit 0.
func TestLiveApply_unmarkedApplyWhoseRecordCannotBeDerivedFails(t *testing.T) {
	_, cloud := unmarkedStore1637Setup(t)

	view, done := testView(t)
	meta := unmarkedStore1637Meta(view, cloud)
	inst := cloud.provider().(*statelessTestProvider)
	assign := inst.MockProvider.ApplyResourceChangeFn
	inst.MockProvider.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		resp := assign(req)
		if req.TypeName != "aws_iam_group_policy" || resp.NewState.IsNull() {
			return resp
		}
		// The provider completes the unknown name and id with null.
		vals := resp.NewState.AsValueMap()
		vals["name"] = cty.NullVal(cty.String)
		vals["id"] = cty.NullVal(cty.String)
		resp.NewState = cty.ObjectVal(vals)
		return resp
	}
	meta.testingOverrides.Providers[addrs.NewDefaultProvider("aws")] = providers.FactoryFixed(inst)

	c := &ApplyCommand{Meta: meta}
	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if code == 0 {
		t.Fatalf("exit 0 after creating aws_iam_group_policy.app with no marker and no record: nothing will ever find this object again:\n%s", all)
	}
	if !strings.Contains(all, "Cannot record a located identity") || !strings.Contains(all, "aws_iam_group_policy.app") {
		t.Fatalf("the run failed, but not with the write-back error naming aws_iam_group_policy.app:\n%s", all)
	}
}
