// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
)

// A concrete parent read through one of its NON-identity attributes.
//
// aws_iam_role's identity is its name. Its arn is not in IdentityAttrs, so
// [Resolution.attrParts] says nothing about it, and its own block never
// writes an arn argument, so [resolver.siblingLiteralExpr] cannot answer
// either. That used to end in "Not an identity attribute", which is the one
// blocker left in two of govuk-infrastructure's deployments.
//
// It is answerable, by the same argument the record-backed branch above it
// already makes and one phase earlier in the same builder: internal/live/
// projection's builder.run materializes every concrete resolution - import,
// then ReadResource - before it renders a single formula, and
// builder.renderFormula's lookup reads an arbitrary attribute off the
// parent's whole provider object with attrString rather than off its
// identity. So what the parent's row says about its identity attributes
// bounds what can be read WITHOUT touching the cloud, which these tests'
// two boundary cases are about, not what can be read at all.

// concreteParentTestSchemas describes the parents whose attributes these
// fixtures read. The children (aws_eks_access_entry, aws_lambda_permission,
// aws_ecs_service, aws_cloudwatch_log_group) deliberately get no entry:
// they are ratified DefaultTable rows and resolve from the table alone.
//
// Every attribute being read is Computed, which is the real shape and also
// the case #220's siblingLiteralExpr refuses on purpose - a provider that
// can invent the value is exactly the provider whose stored object has to
// be the source.
func concreteParentTestSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_iam_role": {
			args: map[string]string{
				"name":               "opt",
				"assume_role_policy": "req",
				"arn":                "comp",
				"unique_id":          "comp",
			},
		},
		"aws_lambda_function": {
			args: map[string]string{
				"function_name": "req",
				"role":          "req",
				"arn":           "comp",
			},
		},
		"aws_ecs_cluster": {
			args: map[string]string{
				"name": "req",
				"arn":  "comp",
			},
		},
		// The two exclusions. Both attributes ARE declared here, so a
		// refusal below is the parent's class refusing and nothing else.
		"aws_instance": {
			args: map[string]string{
				"ami":           "req",
				"instance_type": "req",
				"public_ip":     "comp",
			},
		},
		"aws_api_gateway_rest_api": {
			args: map[string]string{
				"name":             "req",
				"root_resource_id": "comp",
			},
		},
	})
}

const (
	// roleARN is what the projection's lookup returns for the parent's arn
	// once the role has been imported and read.
	roleARN = "arn:aws:iam::123456789012:role/release-assumed"

	// wantEntryID is the marker this fix has to produce.
	wantEntryID = "govuk:" + roleARN

	// dangerousEntryID is what the tempting alternative produces: add "arn"
	// to aws_iam_role's IdentityAttrs and hasIdentityAttr passes, attrParts
	// still cannot answer, and control reaches parentPart's concrete
	// shortcut, which hands back the parent's whole ImportID. That is the
	// role's NAME standing in for its ARN - a marker for a cloud object
	// that does not exist - with every test still green. The assertions
	// below exist to tell the two apart.
	dangerousEntryID = "govuk:release-assumed"
)

// TestConcreteParentAttributeRendersTheAttributeNotTheImportID is the whole
// point: it asserts on the RENDERED identity, not on a class or a boolean.
func TestConcreteParentAttributeRendersTheAttributeNotTheImportID(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	assertNoErrors(t, diags)

	role := resolutionAt(t, result, `aws_iam_role.assumed`)
	if role.Class != ClassConcrete {
		t.Fatalf("aws_iam_role.assumed resolved %s (%s), want CONCRETE", role.Class, role.Reason)
	}
	if role.ImportID != "release-assumed" {
		t.Fatalf("aws_iam_role.assumed's import ID is %q, want %q", role.ImportID, "release-assumed")
	}

	entry := resolutionAt(t, result, `aws_eks_access_entry.assumed`)
	if entry.Class != ClassParentDerived {
		t.Fatalf("aws_eks_access_entry.assumed resolved %s (%s), want PARENT_DERIVED", entry.Class, entry.Reason)
	}

	// The lookup the projection would perform: the role's whole live object
	// is in hand, so arn and name are both readable and they differ.
	live := map[string]string{
		"arn":  roleARN,
		"name": "release-assumed",
	}
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		if parent.String() != `aws_iam_role.assumed` {
			t.Errorf("formula read an unexpected parent %s", parent)
			return "", false
		}
		v, ok := live[attr]
		if !ok {
			t.Errorf("formula read %s.%s, which the role's object does not carry", parent, attr)
		}
		return v, ok
	}

	got, ok := entry.Formula.Render(lookup)
	if !ok {
		t.Fatalf("aws_eks_access_entry.assumed's formula did not render: %v", entry.Formula)
	}
	if got != wantEntryID {
		t.Errorf("aws_eks_access_entry.assumed renders %q, want %q", got, wantEntryID)
	}
	if got == dangerousEntryID {
		t.Errorf("aws_eks_access_entry.assumed renders the role's import ID (%q) where its ARN belongs", got)
	}
}

// TestConcreteParentAttributeMutation is (d): the same rendering, over a
// formula mutated into exactly what the dangerous row-edit produces. If the
// assertion above passed both ways it would not be an assertion, so this
// substitutes the parent's ImportID for the ParentRef part - the concrete
// shortcut's return value, literally - and requires the comparison to fail.
func TestConcreteParentAttributeMutation(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	assertNoErrors(t, diags)

	role := resolutionAt(t, result, `aws_iam_role.assumed`)
	entry := resolutionAt(t, result, `aws_eks_access_entry.assumed`)
	if entry.Class != ClassParentDerived {
		t.Fatalf("aws_eks_access_entry.assumed resolved %s, want PARENT_DERIVED", entry.Class)
	}

	mutated := make([]Part, 0, len(entry.Formula.Parts))
	var substituted int
	for _, p := range entry.Formula.Parts {
		if p.Parent != nil && p.Parent.Attr == "arn" {
			mutated = append(mutated, Part{Literal: role.ImportID})
			substituted++
			continue
		}
		mutated = append(mutated, p)
	}
	if substituted != 1 {
		t.Fatalf("substituted %d parent references, want exactly 1: %v", substituted, entry.Formula.Parts)
	}

	never := func(addrs.AbsResourceInstance, string) (string, bool) {
		t.Error("the mutated formula still reads a parent; the substitution missed one")
		return "", false
	}
	got, ok := (&Formula{Parts: mutated}).Render(never)
	if !ok {
		t.Fatalf("the mutated formula did not render")
	}
	if got != dangerousEntryID {
		t.Fatalf("the mutation produced %q, not the %q it is meant to model; this test is no longer checking anything", got, dangerousEntryID)
	}
	if got == wantEntryID {
		t.Fatal("the wanted marker and the marker the dangerous shortcut produces are the same string; the assertion in the test above cannot tell them apart")
	}
}

// TestTheRowEditProducesTheWrongMarker is the mutation the tempting fix
// would actually be, rather than a mutation of a formula: add "arn" to
// aws_iam_role's IdentityAttrs, which is what a reader reaches for on
// seeing "arn is not an identity attribute of aws_iam_role".
//
// It passes hasIdentityAttr. attrParts still cannot answer, because no
// component of the row supplies arn. Control therefore reaches parentPart's
// concrete shortcut and the child's identity becomes the role's NAME. The
// marker reads cluster:release-assumed and claims a cloud object that does
// not exist, with no diagnostic anywhere. This test exists so that anyone
// who tries it finds out here.
func TestTheRowEditProducesTheWrongMarker(t *testing.T) {
	original, ok := DefaultTable["aws_iam_role"]
	if !ok {
		t.Fatal("aws_iam_role is not in DefaultTable")
	}
	edited := original
	edited.IdentityAttrs = append(append([]string(nil), original.IdentityAttrs...), "arn")
	DefaultTable["aws_iam_role"] = edited
	t.Cleanup(func() { DefaultTable["aws_iam_role"] = original })

	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	assertNoErrors(t, diags)

	entry := resolutionAt(t, result, `aws_eks_access_entry.assumed`)
	if entry.Class != ClassConcrete {
		t.Fatalf("the row edit no longer takes the concrete shortcut (%s); re-derive what it does before trusting this test", entry.Class)
	}
	if entry.ImportID != dangerousEntryID {
		t.Fatalf("the row edit produced %q, not the %q this test documents; the shortcut's behaviour has changed", entry.ImportID, dangerousEntryID)
	}
	if entry.ImportID == wantEntryID {
		t.Fatal("the row edit produced the correct ARN marker, so the fix in resolve.go is not what makes the difference")
	}
	t.Logf("adding arn to aws_iam_role's IdentityAttrs yields the marker %q where %q is wanted, silently", entry.ImportID, wantEntryID)
}

// TestConcreteParentAttributeCoversTheWholeFamily walks the other two
// parent types the same eight corpus sites use. Nothing in parentPart names
// a type: the gate is the parent's class plus [resolver.stringAttrInSchema],
// so a client-named parent of any type is covered the moment a child reads
// an attribute of it.
func TestConcreteParentAttributeCoversTheWholeFamily(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	assertNoErrors(t, diags)

	cases := []struct {
		child      string
		wantParent string
		wantPrefix string
		wantSuffix string
		liveARN    string
		wantID     string
	}{
		{
			child:      `aws_lambda_permission.fdi_events`,
			wantParent: `aws_lambda_function.fdi`,
			liveARN:    "arn:aws:lambda:eu-west-1:123456789012:function:fdi-ingest",
			wantID:     "arn:aws:lambda:eu-west-1:123456789012:function:fdi-ingest/AllowEvents",
		},
		{
			child:      `aws_ecs_service.web`,
			wantParent: `aws_ecs_cluster.main`,
			liveARN:    "arn:aws:ecs:eu-west-1:123456789012:cluster/prod-cluster",
			wantID:     "arn:aws:ecs:eu-west-1:123456789012:cluster/prod-cluster/web",
		},
	}
	for _, tc := range cases {
		parent := resolutionAt(t, result, tc.wantParent)
		if parent.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", tc.wantParent, parent.Class)
			continue
		}
		child := resolutionAt(t, result, tc.child)
		if child.Class != ClassParentDerived {
			t.Errorf("%s resolved %s (%s), want PARENT_DERIVED", tc.child, child.Class, child.Reason)
			continue
		}
		lookup := func(p addrs.AbsResourceInstance, attr string) (string, bool) {
			if p.String() != tc.wantParent || attr != "arn" {
				t.Errorf("%s read %s.%s, want %s.arn", tc.child, p, attr, tc.wantParent)
				return "", false
			}
			return tc.liveARN, true
		}
		got, ok := child.Formula.Render(lookup)
		if !ok {
			t.Errorf("%s's formula did not render", tc.child)
			continue
		}
		if got != tc.wantID {
			t.Errorf("%s renders %q, want %q", tc.child, got, tc.wantID)
		}
		// The parent's own import ID must not be what came back.
		if strings.Contains(got, parent.ImportID) && !strings.Contains(got, "arn:aws:") {
			t.Errorf("%s renders %q, which looks like the parent's import ID rather than its ARN", tc.child, got)
		}
	}
}

// TestConcreteParentUnknownAttributeStillRefused: the branch is a schema
// rule, not a licence to read any name off a concrete parent.
func TestConcreteParentUnknownAttributeStillRefused(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr-unknown"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	if !diags.HasErrors() {
		t.Fatal("reading an attribute aws_iam_role's schema does not declare was accepted")
	}
	if !hasDiag(diags, "Not an identity attribute", "no_such_attribute") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestConcreteParentAttributeNeedsSchemas keeps the rule a rule: with no
// schemas there is no source of truth for what the parent's object carries,
// so the refusal is unchanged from before this fix. It is also why
// internal/live/check's TestIdentityGolden, which resolves every fixture
// without schemas on purpose, records no CHANGED identity for this fix: the
// six lines it gains are the three new fixture directories' parents, and
// every child in them stays refused there for want of a schema.
func TestConcreteParentAttributeNeedsSchemas(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("a concrete parent's non-identity attribute was read with no provider schemas")
	}
	if !hasDiag(diags, "Not an identity attribute", "arn") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestServerAssignedParentAttributeDefersToDiscovery is where GitHub issue
// #346 moved this file's boundary, and the move is worth stating plainly
// because this test asserted the opposite until #346 was decided.
//
// It read "still refused", and its reason was that "ServerAssigned rows never
// resolve CONCRETE, so their objects are not in the projection before a
// formula renders". The second half of that sentence is false on the path
// live-plan actually takes. internal/command/live_plan.go runs marker
// discovery between resolution and projection and then replaces the
// resolution list with discovery's own (merged = disco.Resolutions), in which
// every discovered instance has been rewritten [ClassConcrete] carrying its
// live import ID (internal/live/discovery/result.go). builder.run materializes
// those before it renders a single formula, so a needs-discovery parent's
// whole live object IS in b.live by the time a promise to read one attribute
// off it is read - by the identical mechanism, at the identical point, as for
// a parent that was concrete from the start.
//
// So the two cases are now one rule, and what still refuses is what always
// should have: an attribute the provider's schema does not declare
// (TestConcreteParentUnknownAttributeStillRefused), a run with no schemas to
// check against (TestConcreteParentAttributeNeedsSchemas), and a parent whose
// entry [SynthesizeTypeIdentity] inferred rather than [DefaultTable] ratified
// (TestSynthesizedParentAttributeStillRefused, below).
//
// What a parent discovery does NOT find costs is a missing marker, not a wrong
// one: it stays needs-discovery, builder.run omits it, and
// builder.renderFormula's own parent check then omits this child with
// ReasonParentUnavailable, so the plan proposes creating the child rather than
// binding it to anything guessed. That leg is pinned in
// internal/live/projection's TestFormulaOverUndiscoveredParentIsOmitted.
//
// The child is aws_iam_group, not aws_cloudwatch_log_group as this fixture
// read before GitHub issue #289: that type is taggable and enumerable, so
// its own marker fallback would answer this differently, which is a separate
// and correct behaviour this test is not about. aws_iam_group carries no tags
// argument, so what is exercised below is the parent-class rule alone.
func TestServerAssignedParentAttributeDefersToDiscovery(t *testing.T) {
	schemas := concreteParentTestSchemas()
	for _, want := range []struct{ typeName, attr string }{
		{"aws_instance", "public_ip"},
		{"aws_api_gateway_rest_api", "root_resource_id"},
	} {
		r := &resolver{schemas: schemas}
		if !r.stringAttrInSchema(want.typeName, want.attr) {
			t.Fatalf("this test cannot see what it claims to: %s.%s is not in its schema map, so any verdict below is the schema's and not the class's", want.typeName, want.attr)
		}
	}

	cfg := loadConfig(t, filepath.Join("testdata", "server-assigned-parent-attr"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	for _, want := range []struct{ addr, reads string }{
		{`aws_iam_group.by_ip`, "aws_instance.web.public_ip"},
		{`aws_iam_group.by_root`, "aws_api_gateway_rest_api.api.root_resource_id"},
	} {
		res := resolutionAt(t, result, want.addr)
		if res.Class != ClassParentDerived {
			t.Errorf("%s resolved %s, want PARENT_DERIVED deferring to its discovered parent", want.addr, res.Class)
			continue
		}
		got := res.Formula.String()
		if !strings.Contains(got, want.reads) {
			t.Errorf("%s renders %q, which does not read %s", want.addr, got, want.reads)
		}
	}
}

// TestSynthesizedParentAttributeStillRefused is the condition #346 did NOT
// relax, kept apart from the widening above so a reader can see it is still
// load-bearing: a parent whose entry [SynthesizeTypeIdentity] inferred from
// the provider's identity schema, rather than one [DefaultTable] ratified,
// may not have a second value deferred to it. The classification this whole
// branch rests on - the parent is discovered, imported and read before any
// formula renders - is only as good as that inference, and deferring to it
// stacks an inference on an inference.
func TestSynthesizedParentAttributeStillRefused(t *testing.T) {
	if _, ratified := LookupType("test_synth_parent"); ratified {
		t.Fatal("test_synth_parent is in DefaultTable; this test can see nothing")
	}
	cfg := loadConfig(t, filepath.Join("testdata", "synthesized-parent-attr"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: fakeProviderSchemas(map[string]fakeType{
		"test_synth_parent": {
			args:     map[string]string{"name": "req", "endpoint": "comp"},
			identity: map[string]string{"name": "req"},
		},
		"aws_iam_group": {args: map[string]string{"name": "req"}},
	})})
	if !diags.HasErrors() {
		t.Fatal("an attribute of a parent whose entry was synthesized rather than ratified was accepted")
	}
	if !hasDiag(diags, "Not an identity attribute", "endpoint") {
		t.Errorf("no refusal names endpoint:\n%s", renderDiags(diags))
	}
}

// TestConcreteBranchIsUnreachableForIdentityAttributes is decision (1) as a
// test rather than as a comment. The new branch lives inside
// parentPart's `if !entry.hasIdentityAttr(attrName)` block, so a genuine
// identity attribute of a concrete parent cannot reach it and still takes
// the concrete shortcut: it stays a literal, resolves offline, and needs no
// live read. If that ever stopped holding, every type that works today
// would start deferring to a parent read.
func TestConcreteBranchIsUnreachableForIdentityAttributes(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "concrete-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: concreteParentTestSchemas()})
	assertNoErrors(t, diags)

	// aws_ecs_cluster.main.name IS an identity attribute of the parent, and
	// the sibling reading it must stay concrete.
	entry, ok := LookupType("aws_ecs_cluster")
	if !ok {
		t.Fatal("aws_ecs_cluster is not in DefaultTable; this test can see nothing")
	}
	if !entry.hasIdentityAttr("name") {
		t.Fatal("aws_ecs_cluster no longer carries name in IdentityAttrs; this test can see nothing")
	}
	if entry.hasIdentityAttr("arn") {
		t.Fatal("aws_ecs_cluster now carries arn in IdentityAttrs; the fixture no longer exercises the non-identity path")
	}

	svc := resolutionAt(t, result, `aws_ecs_service.web`)
	if svc.Class != ClassParentDerived {
		t.Fatalf("aws_ecs_service.web resolved %s, want PARENT_DERIVED", svc.Class)
	}
	var reads []string
	for _, p := range svc.Formula.Parts {
		if p.Parent != nil {
			reads = append(reads, p.Parent.Attr)
		}
	}
	if len(reads) != 1 || reads[0] != "arn" {
		t.Errorf("aws_ecs_service.web defers reads of %v; only the non-identity arn should be deferred", reads)
	}
}
