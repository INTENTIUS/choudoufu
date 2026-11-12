// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configadjust

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// seenAdjuster records whether the value it was asked about was marked as a
// whole, and stamps a key only when it was not, as the real adjuster does.
type seenAdjuster struct{ sawMarked bool }

func (a *seenAdjuster) AdjustConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	if config.IsMarked() {
		a.sawMarked = true
		return config, nil
	}
	return cty.ObjectVal(map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("e")})}), nil
}

func noSchema(context.Context) (providers.Schema, tfdiags.Diagnostics) {
	return providers.Schema{}, nil
}

// TestAdjustPeelsEngineMarks: a bookkeeping mark on the whole configuration
// (objchange.PendingChange here, which depends_on hoists there on the new
// runtime) does not stop the adjuster, and is back on what Adjust returns.
func TestAdjustPeelsEngineMarks(t *testing.T) {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "t", Name: "n"}.Absolute(addrs.RootModuleInstance).Instance(addrs.NoKey)
	pending := objchange.PendingChange{}
	config := cty.ObjectVal(map[string]cty.Value{"tags": cty.NullVal(cty.Map(cty.String))}).Mark(pending)

	adj := &seenAdjuster{}
	got, diags := Adjust(context.Background(), adj, addr, config, cty.NilVal, noSchema)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if adj.sawMarked {
		t.Error("the adjuster was handed the engine's mark")
	}
	if !got.Config.HasMark(pending) || !got.CreateConfig.HasMark(pending) {
		t.Errorf("the engine's mark was not put back: %#v", got.Config)
	}
	inner, _ := got.Config.Unmark()
	if tags := inner.GetAttr("tags"); tags.IsNull() {
		t.Error("the adjuster's stamp is missing")
	}
}

// TestAdjustKeepsSensitiveMark: a configuration sensitive as a whole still
// reaches the adjuster marked, so it can decline to write a marker.
func TestAdjustKeepsSensitiveMark(t *testing.T) {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "t", Name: "n"}.Absolute(addrs.RootModuleInstance).Instance(addrs.NoKey)
	config := cty.ObjectVal(map[string]cty.Value{"tags": cty.NullVal(cty.Map(cty.String))}).Mark(marks.Sensitive).Mark(objchange.PendingChange{})

	adj := &seenAdjuster{}
	got, diags := Adjust(context.Background(), adj, addr, config, cty.NilVal, noSchema)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !adj.sawMarked {
		t.Error("a sensitive configuration reached the adjuster unmarked")
	}
	if !got.Config.HasMark(marks.Sensitive) {
		t.Error("the sensitive mark was lost")
	}
}
