// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1108. [builder.checkOwnership] read one marker surface, the
// AWS tag map, so [markerCapable] answered false for every Kubernetes type
// and the switch admitted the live object before any label was read. A
// declared block bound its object by natural key whether the object carried
// this estate's label, another estate's, or none - so the plan proposed
// relabelling somebody else's object to this estate (only #1066's admission
// policy, where a cluster admin has installed it, stood between that plan
// and the write) and adopted an unlabelled one with nothing said. The
// ownership policy's declared quadrants, documented for Kubernetes, never
// executed there at all.
//
// These tests are the regression. Every one of them is proved red by
// restoring the tags-only body of [markerSurfaceOf]:
//
//	func markerSurfaceOf(block *configschema.Block) markerSurface {
//		if block == nil {
//			return surfaceNone
//		}
//		for _, name := range []string{"tags", "tags_all"} {
//			if _, ok := block.Attributes[name]; ok {
//				return surfaceTags
//			}
//		}
//		return surfaceNone
//	}
//
// They call [builder.checkOwnership] directly rather than through
// [BuildWith]: the package's fake cloud is an AWS caricature whose objects
// are flat string attributes and a tags map, with no nested-block support
// at all, so the values a Kubernetes surface is read out of - a metadata
// block of list nesting, a dynamic manifest - cannot be expressed in it.
// The schemas here are hashicorp/kubernetes 3.2.1's own, shared with the
// stamp's tests (configMapTypeSchema, manifestTypeSchema).

const k8sOwnershipEstate = "smoke-k8s"

// k8sOwnershipBuilder is a builder with just the fields checkOwnership and
// its two recorders touch.
func k8sOwnershipBuilder(own *Ownership) *builder {
	return &builder{
		opts:    Options{Ownership: own},
		omitted: map[string]Omission{},
		causes:  map[string]string{},
	}
}

// k8sLiveConfigMap is the object hashicorp/kubernetes hands back for a
// kubernetes_config_map: metadata is a one-element list, labels a
// map(string), null when the object carries none.
func k8sLiveConfigMap(labels map[string]string) cty.Value {
	var labelVal cty.Value
	if labels == nil {
		labelVal = cty.NullVal(cty.Map(cty.String))
	} else {
		vals := make(map[string]cty.Value, len(labels))
		for k, v := range labels {
			vals[k] = cty.StringVal(v)
		}
		labelVal = cty.MapVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"data":      cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"id":        cty.StringVal("smoke-k8s/app-config"),
		"immutable": cty.NullVal(cty.Bool),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": cty.NullVal(cty.Map(cty.String)),
			"labels":      labelVal,
			"name":        cty.StringVal("app-config"),
			"namespace":   cty.StringVal("smoke-k8s"),
		})}),
	})
}

// k8sLiveManifest is what a kubernetes_manifest instance looks like on the
// read path at the point ownership is checked: the prior manifest the
// projection seeded and stamped with THIS estate's label, and the live
// object the provider read back carrying priorLabels. It returns the value
// after [mirrorManifestComputedFields], because that is the order build.go runs
// them in (readImported mirrors, materialize then checks ownership), and
// the mirror is what puts the live object's own answer for the marker key
// into the manifest this surface reads.
func k8sLiveManifest(t *testing.T, stampedEstate string, liveLabels map[string]string) cty.Value {
	t.Helper()
	manifest := manifestTestManifest(cty.ObjectVal(map[string]cty.Value{
		markers.TagEstate: cty.StringVal(stampedEstate),
	}))

	liveMeta := map[string]cty.Value{
		"name":      cty.StringVal("my-crontab"),
		"namespace": cty.StringVal("smoke-crd"),
	}
	if liveLabels == nil {
		liveMeta["labels"] = cty.NullVal(cty.Map(cty.String))
	} else {
		vals := make(map[string]cty.Value, len(liveLabels))
		for k, v := range liveLabels {
			vals[k] = cty.StringVal(v)
		}
		liveMeta["labels"] = cty.MapVal(vals)
	}
	object := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata":   cty.ObjectVal(liveMeta),
	})

	v := cty.ObjectVal(map[string]cty.Value{
		"manifest":        manifest,
		"object":          object,
		"computed_fields": cty.NullVal(cty.List(cty.String)),
		"field_manager":   cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String})),
		"wait":            cty.ListValEmpty(cty.Object(map[string]cty.Type{"rollout": cty.Bool})),
	})
	return mirrorManifestComputedFields(v, manifestTypeSchema().Block)
}

func k8sConfigMapAddr(t *testing.T) addrs.AbsResourceInstance {
	t.Helper()
	return configMapAddr(t)
}

// TestK8sOwnership_surfaceIsReadFromTheSchema pins the classification the
// rest of these tests rest on, against the provider's own schemas rather
// than against a list of type names.
func TestK8sOwnership_surfaceIsReadFromTheSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		block *configschema.Block
		want  markerSurface
	}{
		"kubernetes_config_map": {configMapTypeSchema().Block, surfaceLabels},
		"kubernetes_manifest":   {manifestTypeSchema().Block, surfaceManifest},
		"aws taggable":          {fakeSchemas()["aws_cloudwatch_log_group"].Block, surfaceTags},
		"nil":                   {nil, surfaceNone},
	} {
		if got := markerSurfaceOf(tc.block); got != tc.want {
			t.Errorf("%s: markerSurfaceOf = %d, want %d", name, got, tc.want)
		}
		if got, want := markerCapable(tc.block), tc.want != surfaceNone; got != want {
			t.Errorf("%s: markerCapable = %v, want %v", name, got, want)
		}
	}
}

// TestK8sOwnership_anotherEstatesObjectIsRefused is the defect itself, on
// the metadata-block surface. A ConfigMap at the namespace and name this
// block declares carries tofu-estate=other. The plan must refuse it by
// name - from the plan side, with nothing in the cluster consulted - and
// the sentence must be the one AWS uses, naming the estate that owns it.
func TestK8sOwnership_anotherEstatesObjectIsRefused(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
	addr := k8sConfigMapAddr(t)

	verdict := b.checkOwnership(addr, configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}),
		true, false, false)

	if verdict != ownershipUnowned {
		t.Fatalf("checkOwnership = %d, want ownershipUnowned (%d): another estate's object was admitted, and the plan would propose relabelling it to this estate", verdict, ownershipUnowned)
	}
	if len(b.unownedList) != 1 {
		t.Fatalf("Unowned entries = %+v, want exactly one", b.unownedList)
	}
	if got := b.unownedList[0].Estate; got != "other" {
		t.Errorf("the refusal reports estate %q, want %q", got, "other")
	}
	detail := b.unownedList[0].Detail
	for _, want := range []string{"smoke-k8s/app-config", markers.TagEstate + "=\"other\"", "belongs to another estate"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, detail)
		}
	}
	if !hasDiag(b.diags, SummaryOutsideEstate, "other") {
		t.Errorf("the refusal produced no warning an operator would see:\n%s", renderDiags(b.diags))
	}
	if _, omitted := b.omitted[addr.String()]; !omitted {
		t.Errorf("the instance was not omitted from the projection: %+v", b.omitted)
	}
}

// TestK8sOwnership_thisEstatesObjectIsAdmitted is the other half: a rule
// that refused every Kubernetes object would be safe and useless.
func TestK8sOwnership_thisEstatesObjectIsAdmitted(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: k8sOwnershipEstate, "app": "web"}),
		true, false, false)

	if verdict != ownershipOK {
		t.Fatalf("checkOwnership = %d, want ownershipOK: this estate's own labelled object was refused (%+v)", verdict, b.unownedList)
	}
	if len(b.unownedList) != 0 {
		t.Errorf("an admitted object was also reported unowned: %+v", b.unownedList)
	}
	if len(b.policyList) != 0 {
		t.Errorf("the default declared_tagged verb recorded a policy outcome: %+v", b.policyList)
	}
}

// TestK8sOwnership_unlabelledObjectIsDeclaredUntagged: an object with no
// estate label is the declared_untagged quadrant, refused by default with
// the sentence that says how to adopt it - and, per #1016, that sentence
// names one label and no address, because the Kubernetes marker is the
// estate alone.
func TestK8sOwnership_unlabelledObjectIsDeclaredUntagged(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false)

	if verdict != ownershipUnowned {
		t.Fatalf("checkOwnership = %d, want ownershipUnowned: an unlabelled object was adopted with nothing said", verdict)
	}
	if len(b.unownedList) != 1 || b.unownedList[0].Estate != "" {
		t.Fatalf("Unowned entries = %+v, want one entry carrying no estate", b.unownedList)
	}
	detail := b.unownedList[0].Detail
	for _, want := range []string{
		"carries no " + markers.TagEstate + " label",
		"Adopt it by writing the label " + markers.TagEstate + "=\"" + k8sOwnershipEstate + "\"",
		`policy { declared_untagged = "adopt" }`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, markers.TagAddress) {
		t.Errorf("the Kubernetes refusal tells the operator to write a %s marker, which #1016 ruled does not exist on this substrate:\n%s", markers.TagAddress, detail)
	}
}

// TestK8sOwnership_policyVerbsReachTheLabelSurface: the point of reading
// the label is that the matrix then applies unchanged. declared_untagged =
// "adopt" admits the unlabelled object and records the outcome, exactly as
// it does on a tags surface.
func TestK8sOwnership_policyVerbsReachTheLabelSurface(t *testing.T) {
	for _, verb := range []string{"adopt", "converge"} {
		t.Run(verb, func(t *testing.T) {
			pol := buildPolicy(t, "", verb)
			b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: pol})

			verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
				configMapTypeSchema(), k8sLiveConfigMap(nil), true, false, false)

			if verdict != ownershipOK {
				t.Fatalf("declared_untagged = %q did not admit the object: verdict %d, %+v", verb, verdict, b.unownedList)
			}
			if len(b.policyList) != 1 || b.policyList[0].Verb != policy.Verb(verb) || b.policyList[0].Tagged {
				t.Fatalf("policy outcomes = %+v, want one declared_untagged=%s entry", b.policyList, verb)
			}
		})
	}
}

// TestK8sOwnership_anotherEstateUnderAdoptMatchesTheTagSurface pins the
// property this unit actually promises - the matrix applies to a label
// surface EXACTLY as it applies to a tags surface - on the one input where
// that is uncomfortable.
//
// [checkOwnership] reads "untagged" as "does not carry THIS estate's
// marker", so an object carrying ANOTHER estate's marker lands in the
// declared_untagged quadrant and `policy { declared_untagged = "adopt" }`
// admits it, after which stamping writes this estate's marker over the
// other estate's. That is not something #1108 introduced and it is not a
// property of Kubernetes: the identical input on aws_cloudwatch_log_group
// through BuildWith admits the resource and records
// declared_untagged=adopt, measured on this tree before the fix. It is
// filed separately as GitHub issue #1166; what belongs here is the
// symmetry, so that whichever way that ruling goes, it moves both surfaces
// together rather than leaving Kubernetes with a policy matrix of its own.
func TestK8sOwnership_anotherEstateUnderAdoptMatchesTheTagSurface(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "adopt")})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}),
		true, false, false)

	if verdict != ownershipOK {
		t.Fatalf("the label surface refused under declared_untagged = adopt where the tag surface admits: verdict %d. The two surfaces must share one matrix.", verdict)
	}
	if len(b.policyList) != 1 || b.policyList[0].Verb != policy.Adopt || b.policyList[0].Tagged {
		t.Fatalf("policy outcomes = %+v, want one declared_untagged=adopt entry, as the tag surface records", b.policyList)
	}

	// With no policy block at all - the shipped default - the same object
	// is refused. The adoption above is something an operator asked for in
	// writing.
	def := k8sOwnershipBuilder(&Ownership{Estate: policyEstate})
	if got := def.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}),
		true, false, false); got != ownershipUnowned {
		t.Fatalf("the default refused nothing: verdict %d", got)
	}
}

// TestK8sOwnership_undeclaredObjectStillReadsItsLabel: the marker read is
// not conditional on the instance being declared. An undeclared instance
// reaching this function (a sweep orphan, a parent-read finding) carrying
// another estate's label is refused, and no declared-quadrant policy
// outcome is recorded for it.
func TestK8sOwnership_undeclaredObjectStillReadsItsLabel(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}),
		false, false, false)

	if verdict != ownershipUnowned {
		t.Fatalf("an undeclared instance's object was admitted without its label being read: verdict %d", verdict)
	}
	if len(b.policyList) != 0 {
		t.Errorf("an undeclared instance was given a declared-quadrant verb: %+v", b.policyList)
	}
}

// TestK8sOwnership_aStrayAddressLabelIsNotAClaim: #1016 ruled the
// Kubernetes marker is the estate label alone - no tofu-address, because
// the object's own kind, namespace and name are the join key. So the
// second half of the ownership question is not asked on this surface, and
// a stray tofu-address label somebody wrote by hand is not read as a
// competing claim. On the tag surface the identical shape IS a refusal
// (TestOwnershipAddress_*), which is what makes this worth pinning.
func TestK8sOwnership_aStrayAddressLabelIsNotAClaim(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{
			markers.TagEstate:  k8sOwnershipEstate,
			markers.TagAddress: "kubernetes_config_map.somebody_else",
		}),
		true, false, false)

	if verdict != ownershipOK {
		t.Fatalf("a stray %s label was read as another instance's claim: verdict %d, %+v", markers.TagAddress, verdict, b.unownedList)
	}
}

// TestK8sOwnership_recordFirstDoesNotDemandAnAddressLabel: the
// stale-record rule (#389) verifies a record-held binding against the live
// object's own tofu-address. There is no such marker on Kubernetes, so
// asking for it would make every record-bound Kubernetes instance read as
// stale on every run. The estate label is still checked.
func TestK8sOwnership_recordFirstDoesNotDemandAnAddressLabel(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})

	verdict := b.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: k8sOwnershipEstate}),
		true, false, true)

	if verdict != ownershipOK {
		t.Fatalf("a record-bound Kubernetes instance carrying this estate's label read as %d, want ownershipOK", verdict)
	}
	if len(b.diags) != 0 {
		t.Errorf("a stale-record warning was raised for a label surface that has no address marker:\n%s", renderDiags(b.diags))
	}

	// And the estate label is still the gate: another estate's label under
	// recordFirst is refused, not merely reported stale.
	b2 := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
	if got := b2.checkOwnership(k8sConfigMapAddr(t), configMapTestType, "smoke-k8s/app-config",
		configMapTypeSchema(),
		k8sLiveConfigMap(map[string]string{markers.TagEstate: "other"}),
		true, false, true); got != ownershipUnowned {
		t.Fatalf("a record-bound instance on another estate's object read as %d, want ownershipUnowned", got)
	}
}

// TestK8sOwnership_manifestSurfaceReadsItsLabel is the same defect on
// kubernetes_manifest, the type every custom resource is declared through.
// The value under test is the one build.go's read path produces: the
// stamped prior manifest with [mirrorManifestComputedFields] having carried the
// live object's own answer for the marker key into it.
func TestK8sOwnership_manifestSurfaceReadsItsLabel(t *testing.T) {
	addr := manifestAddr(t)
	for name, tc := range map[string]struct {
		liveLabels map[string]string
		want       ownershipVerdict
		wantEstate string
	}{
		"another estate": {map[string]string{markers.TagEstate: "other"}, ownershipUnowned, "other"},
		"no label":       {nil, ownershipUnowned, ""},
		"this estate":    {map[string]string{markers.TagEstate: k8sOwnershipEstate}, ownershipOK, ""},
	} {
		t.Run(name, func(t *testing.T) {
			b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
			obj := k8sLiveManifest(t, k8sOwnershipEstate, tc.liveLabels)

			verdict := b.checkOwnership(addr, "kubernetes_manifest", "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab",
				manifestTypeSchema(), obj, true, false, false)

			if verdict != tc.want {
				t.Fatalf("checkOwnership = %d, want %d (unowned: %+v)", verdict, tc.want, b.unownedList)
			}
			if tc.want != ownershipUnowned {
				return
			}
			if len(b.unownedList) != 1 || b.unownedList[0].Estate != tc.wantEstate {
				t.Fatalf("Unowned entries = %+v, want one carrying estate %q", b.unownedList, tc.wantEstate)
			}
		})
	}
}

// TestK8sOwnership_markerlessTypeIsStillAdmitted: the boundary the fix must
// not move. A type with nowhere to carry a marker is admitted, because its
// ownership is its parents' - see [checkOwnership]'s doc comment. The
// Kubernetes surfaces are additions to the read, not a new refusal for
// types that have no surface at all.
func TestK8sOwnership_markerlessTypeIsStillAdmitted(t *testing.T) {
	b := k8sOwnershipBuilder(&Ownership{Estate: k8sOwnershipEstate})
	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":         {Type: cty.String, Computed: true},
			"group":      {Type: cty.String, Required: true},
			"policy_arn": {Type: cty.String, Required: true},
		},
	}}
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":         cty.StringVal("group/arn"),
		"group":      cty.StringVal("group"),
		"policy_arn": cty.StringVal("arn"),
	})

	if got := b.checkOwnership(k8sConfigMapAddr(t), "aws_iam_group_policy_attachment", "group/arn",
		schema, obj, true, false, false); got != ownershipOK {
		t.Fatalf("a markerless type was refused: verdict %d, %+v", got, b.unownedList)
	}
}
