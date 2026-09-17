// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1192. The smoke claim proves the whole path on a real
// cluster and is the evidence that matters; these are the cases a kind
// cluster cannot reach cheaply, and the AWS half in particular.
//
// The AWS half needs saying because the live estates cannot prove it on
// their own. corpus-sqs-basic and corpus-vpc-complete run this hook over
// every tagged resource they apply and it stays silent, which is the right
// answer - but silence is also what a hook that bailed out at its first
// guard would produce. TestVerifyAppliedMarkersTagSurface is what
// distinguishes the two: the same AWS-shaped schema and values, one pair
// that must fire and one that must not.

func awsTagSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		},
	}}
}

func k8sLabelSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting:  configschema.NestingList,
				MinItems: 1,
				MaxItems: 1,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":   {Type: cty.String, Optional: true},
						"labels": {Type: cty.Map(cty.String), Optional: true},
					},
				},
			},
		},
	}}
}

func awsObj(tags, tagsAll cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("i-123"),
		"tags":     tags,
		"tags_all": tagsAll,
	})
}

func k8sObj(labels cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("ns/app"),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":   cty.StringVal("app"),
			"labels": labels,
		})}),
	})
}

func tagMap(kv map[string]string) cty.Value {
	if len(kv) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	out := make(map[string]cty.Value, len(kv))
	for k, v := range kv {
		out[k] = cty.StringVal(v)
	}
	return cty.MapVal(out)
}

func verifyFixture() (*NodeResolver, addrs.AbsResourceInstance) {
	n := &NodeResolver{Estate: "prod"}
	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "aws_sqs_queue",
		Name: "q",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return n, addr
}

func severityOf(t *testing.T, diags tfdiags.Diagnostics) (tfdiags.Severity, string) {
	t.Helper()
	if len(diags) != 1 {
		t.Fatalf("want exactly one diagnostic, got %d: %s", len(diags), diags.Err())
	}
	d := diags[0].Description()
	if d.Summary != SummaryMarkerNotStored {
		t.Fatalf("diagnostic is %q, want %q", d.Summary, SummaryMarkerNotStored)
	}
	return diags[0].Severity(), d.Detail
}

// TestVerifyAppliedMarkersTagSurface is the AWS blast-radius check. The
// estates prove the hook stays quiet over a real apply; this proves the
// quiet is a verdict and not a hook that never looked, and pins the two
// shapes a false positive would come from - tags_all standing in for tags,
// and a provider that returns the tags attribute as null.
func TestVerifyAppliedMarkersTagSurface(t *testing.T) {
	n, addr := verifyFixture()
	schema := awsTagSchema()
	marked := map[string]string{"Name": "q", "tofu-estate": "prod", "tofu-address": "aws_sqs_queue.q"}

	t.Run("stripped tag fires", func(t *testing.T) {
		// The AWS shape of #1192: an Organizations tag policy removed the
		// marker, and the provider read the stored tags back.
		diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update,
			awsObj(tagMap(marked), tagMap(marked)),
			awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(map[string]string{"Name": "q"})),
			schema)
		sev, detail := severityOf(t, diags)
		if sev != tfdiags.Error {
			t.Errorf("an update that lost tofu-estate must be an error, got severity %v", sev)
		}
		for _, want := range []string{`tofu-estate: sent "prod", not stored`, `tofu-address: sent "aws_sqs_queue.q", not stored`} {
			if !strings.Contains(detail, want) {
				t.Errorf("detail does not name %q:\n%s", want, detail)
			}
		}
	})

	t.Run("marker stored is silent", func(t *testing.T) {
		v := awsObj(tagMap(marked), tagMap(marked))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Create, v, v, schema); len(diags) != 0 {
			t.Fatalf("a marker that landed must say nothing, got: %s", diags.Err())
		}
	})

	t.Run("marker arriving only through tags_all is silent", func(t *testing.T) {
		// default_tags supply the markers: `tags` carries none of them and
		// `tags_all` carries all of them, on both sides. Reading only
		// `tags` would make this a false positive on every such estate.
		planned := awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(marked))
		applied := awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(marked))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("markers carried by tags_all must not read as missing, got: %s", diags.Err())
		}
	})

	t.Run("provider that returns null tags is silent", func(t *testing.T) {
		// The other false-positive shape, and the reason carrierMarkers
		// insists on a non-null carrier: a provider that does not echo its
		// tags is an absence of information, never evidence of a strip.
		planned := awsObj(tagMap(marked), tagMap(marked))
		applied := awsObj(cty.NullVal(cty.Map(cty.String)), cty.NullVal(cty.Map(cty.String)))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("a null tags map must not be read as a stripped marker, got: %s", diags.Err())
		}
	})

	t.Run("unknown tags after apply is silent", func(t *testing.T) {
		planned := awsObj(tagMap(marked), tagMap(marked))
		applied := awsObj(cty.UnknownVal(cty.Map(cty.String)), cty.UnknownVal(cty.Map(cty.String)))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("an unknown tags map must not be read as a stripped marker, got: %s", diags.Err())
		}
	})

	t.Run("marked carrier is silent and leaks nothing", func(t *testing.T) {
		// A marked tags map holding the marker is a value this fork's own
		// stamp produces: stampedTags unmarks, writes, and re-marks. The
		// check must read it as unreadable rather than as empty - reading
		// it through an iterator would drop the mark, yield exactly the
		// plain map a stripped carrier yields, and put the values into a
		// printed diagnostic.
		sensitive := cty.NewValueMarks("sensitive")
		planned := awsObj(tagMap(marked).WithMarks(sensitive), tagMap(marked).WithMarks(sensitive))
		applied := awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(map[string]string{"Name": "q"}))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("a marked planned carrier must be read as unreadable, not as a strip: %s", diags.Err())
		}

		// The other direction, and the one that would leak: the marker was
		// sent in the clear and the object came back with a marked tags
		// map. Nothing may be reported, and nothing may be printed.
		planned2 := awsObj(tagMap(marked), tagMap(marked))
		applied2 := awsObj(tagMap(map[string]string{"Name": "q"}).WithMarks(sensitive), tagMap(map[string]string{"Name": "q"}).WithMarks(sensitive))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned2, applied2, schema); len(diags) != 0 {
			t.Fatalf("a marked applied carrier must be read as unreadable, not as a strip: %s", diags.Err())
		}
	})

	t.Run("a marked tags attribute abandons the whole carrier", func(t *testing.T) {
		// tags_all is readable and carries no marker; tags is marked and
		// is where the marker lives. Skipping only the marked attribute
		// would report the marker missing - a false positive assembled out
		// of a mark - so the whole carrier is abandoned instead.
		sensitive := cty.NewValueMarks("sensitive")
		planned := awsObj(tagMap(marked), tagMap(marked))
		applied := awsObj(tagMap(marked).WithMarks(sensitive), tagMap(map[string]string{"Name": "q"}))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("a marker hidden in a marked tags map must not read as missing from tags_all: %s", diags.Err())
		}
	})

	t.Run("nothing stamped is silent", func(t *testing.T) {
		// An estate with no marker in the planned value - a record-rung
		// selection, or a policy untag - has nothing to check.
		plain := tagMap(map[string]string{"Name": "q"})
		v := awsObj(plain, plain)
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, v, v, schema); len(diags) != 0 {
			t.Fatalf("an unstamped instance must say nothing, got: %s", diags.Err())
		}
	})

	t.Run("no estate is silent", func(t *testing.T) {
		none := &NodeResolver{}
		planned := awsObj(tagMap(marked), tagMap(marked))
		applied := awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(map[string]string{"Name": "q"}))
		if diags := none.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("a run with no estate stamps nothing and must judge nothing, got: %s", diags.Err())
		}
	})

	t.Run("delete is silent", func(t *testing.T) {
		planned := awsObj(tagMap(marked), tagMap(marked))
		applied := awsObj(tagMap(map[string]string{"Name": "q"}), tagMap(map[string]string{"Name": "q"}))
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Delete, planned, applied, schema); len(diags) != 0 {
			t.Fatalf("a delete has no marker to keep, got: %s", diags.Err())
		}
	})

	t.Run("rewritten value fires and quotes both", func(t *testing.T) {
		planned := awsObj(tagMap(marked), tagMap(marked))
		rewritten := map[string]string{"Name": "q", "tofu-estate": "sandbox", "tofu-address": "aws_sqs_queue.q"}
		applied := awsObj(tagMap(rewritten), tagMap(rewritten))
		_, detail := severityOf(t, n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema))
		if !strings.Contains(detail, `tofu-estate: sent "prod", stored "sandbox"`) {
			t.Errorf("detail does not quote both values:\n%s", detail)
		}
	})
}

// TestVerifyAppliedMarkersSeveritySplit pins the decision #1192 turns on:
// a create warns, an update that lost the estate marker is an error. The
// live claim proves it end to end; this proves the rule itself, so a change
// to it is a change someone made on purpose.
func TestVerifyAppliedMarkersSeveritySplit(t *testing.T) {
	n, addr := verifyFixture()
	schema := k8sLabelSchema()
	planned := k8sObj(tagMap(map[string]string{"tofu-estate": "prod"}))
	applied := k8sObj(cty.MapValEmpty(cty.String))

	t.Run("create warns", func(t *testing.T) {
		sev, detail := severityOf(t, n.VerifyAppliedMarkers(context.Background(), addr, plans.Create, planned, applied, schema))
		if sev != tfdiags.Warning {
			t.Errorf("a create must warn, not refuse: the object exists and something has to say so (severity %v)", sev)
		}
		if !strings.Contains(detail, "was created") {
			t.Errorf("detail does not say the object was created:\n%s", detail)
		}
	})

	t.Run("update errors", func(t *testing.T) {
		sev, detail := severityOf(t, n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, applied, schema))
		if sev != tfdiags.Error {
			t.Errorf("an update that lost the estate marker must be an error; exit 0 for ever is the defect (severity %v)", sev)
		}
		if !strings.Contains(detail, "Nothing this run wrote to that object lasted") {
			t.Errorf("detail does not say the write accomplished nothing:\n%s", detail)
		}
	})

	t.Run("absent labels map is silent", func(t *testing.T) {
		if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Update, planned, k8sObj(cty.NullVal(cty.Map(cty.String))), schema); len(diags) != 0 {
			t.Fatalf("a null labels map is no information, got: %s", diags.Err())
		}
	})
}
