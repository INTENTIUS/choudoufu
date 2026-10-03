// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1708: the post-create questions take the created instance
// and the run's per-family facts, never AWS's registry read or an arn the
// shared node path pulled off the object.

// gadgetSurface is a synthetic family's surface: nothing here may be
// answered by AWS's registry or phrased in AWS's words.
const gadgetSurface markers.Surface = "gadget-bind-surface"

// gadgetRules is the gadget family's own facts: which types bind their
// marker after the create. Not a registry, not a roster.
type gadgetRules map[string]bool

// gadgetFamily needs neither a registry nor an arn. It decides from its
// own entry in [substrate.Facts], and addresses an object by its
// self_link.
type gadgetFamily struct{ substrate.Substrate }

func (gadgetFamily) Name() string                { return "gadget" }
func (gadgetFamily) Surfaces() []markers.Surface { return []markers.Surface{gadgetSurface} }
func (gadgetFamily) MarkerWriter() substrate.Write {
	return "gadget-bind"
}
func (gadgetFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if _, ok := block.Attributes["self_link"]; ok {
		return gadgetSurface, true
	}
	return "", false
}
func (gadgetFamily) Writes(surface markers.Surface) substrate.Writes {
	if surface == gadgetSurface {
		return substrate.Writes{Create: substrate.WriteInCreate, Adopt: "gadget-bind", PostCreate: "gadget-bind"}
	}
	return substrate.Writes{}
}
func (f gadgetFamily) PostCreateNeeded(surface markers.Surface, created substrate.Created, facts substrate.Facts) (string, bool) {
	rules, _ := facts.Of(f.Name()).(gadgetRules)
	if surface != gadgetSurface || !rules[created.Type()] {
		return "", false
	}
	return created.Type() + " binds its marker after the create (gadget rules)", true
}
func (gadgetFamily) ManualMarkFix(created substrate.Created, want map[string]string, _ substrate.Facts) string {
	return fmt.Sprintf("Run: gadgetctl bind %s %s", substrate.ObjectString(created.Object, "self_link"), markers.TagsArgument(want))
}
func (gadgetFamily) CreatedObject(created substrate.Created) string {
	return substrate.ObjectString(created.Object, "self_link")
}

func gadgetSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"name":      {Type: cty.String, Required: true},
			"self_link": {Type: cty.String, Computed: true},
		},
	}}
}

// TestPostCreate_aFamilyNeedingNoRegistryAndNoARN: the gadget family's
// create is withheld and marked from its own facts, and a failed write is
// described and remedied in its own words - no arn anywhere in the
// diagnostic, and no AWS roster in the run at all.
func TestPostCreate_aFamilyNeedingNoRegistryAndNoARN(t *testing.T) {
	saved := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), saved...), gadgetFamily{substrate.AWS})
	t.Cleanup(func() { substrate.All = saved })

	applied := cty.ObjectVal(map[string]cty.Value{
		"name":      cty.StringVal("thing"),
		"self_link": cty.StringVal("//gadget/things/G1"),
	})
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("gadget")}
	thing := locatedTestAddr(t, "gadget_thing", "x")

	// Facts injected once, the gadget family's entry only: no AWS roster.
	n := &NodeResolver{Estate: "prod", Facts: substrate.Facts{"gadget": gadgetRules{"gadget_thing": true}}}
	n.MarkerWriter = func(addrs.AbsProviderConfig, substrate.Write) (MarkerWriter, error) {
		return failingWriter{err: errors.New("refused by test")}, nil
	}

	_, diags := n.WriteAppliedMarkers(context.Background(), thing, provider, plans.Create, applied, gadgetSchema())
	if !diags.HasErrors() {
		t.Fatal("the gadget family's own facts did not reach its post-create question: nothing was written")
	}
	detail := diags[0].Description().Detail
	for _, want := range []string{
		"gadget_thing.x was created as //gadget/things/G1 and could not be marked: refused by test.",
		"gadget_thing binds its marker after the create (gadget rules), so this run withheld",
		"Run: gadgetctl bind //gadget/things/G1 'tofu-address=gadget_thing.x,tofu-estate=prod'",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail does not carry %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, "arn") {
		t.Errorf("the shared path phrased a non-AWS object in AWS's words:\n%s", detail)
	}

	// The same family with no facts of its own takes the create-call path:
	// the answer came from its entry, not from anything the run holds for
	// another family.
	bare := &NodeResolver{Estate: "prod", Facts: tocFacts(t), MarkerWriter: n.MarkerWriter}
	if _, diags := bare.WriteAppliedMarkers(context.Background(), thing, provider, plans.Create, applied, gadgetSchema()); diags.HasErrors() {
		t.Errorf("with no gadget facts the create was still withheld: %v", diags.Err())
	}
}

// TestCreatedObject_awsWordingUnchanged pins the three object phrasings
// #1084 built inline in the node path, now the AWS family's answer.
func TestCreatedObject_awsWordingUnchanged(t *testing.T) {
	after := locatedTestAddr(t, "aws_after_thing", "x")
	for _, tc := range []struct{ arn, id, want string }{
		{"arn:aws:after:::thing/T1", "T1", "arn:aws:after:::thing/T1 [id=T1]"},
		{"arn:aws:after:::thing/T1", "arn:aws:after:::thing/T1", "arn:aws:after:::thing/T1"},
		{"", "T1", "[id=T1]"},
		{"", "", "an object with no arn and no id in what the provider returned"},
	} {
		got := substrate.CreatedObject(markers.SurfaceTags, substrate.Created{Addr: after, Object: tocApplied(tc.arn, tc.id, nil)})
		if got != tc.want {
			t.Errorf("arn %q id %q: got %q, want %q", tc.arn, tc.id, got, tc.want)
		}
	}
}
