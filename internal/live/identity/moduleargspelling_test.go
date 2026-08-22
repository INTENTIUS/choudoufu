// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestModuleArgumentSpellingIsNotIdentity is the resolving half of issue
// #375's fix, and its claim is an EQUIVALENCE rather than a new answer.
//
// Three ways of handing a child module the same map:
//
//	base_configuration = { enabled = true, label = "hoisted", subnet = aws_subnet.s.id }
//	locals { hoisted = { ... } }   base_configuration = local.hoisted
//	base_configuration = module.net.configuration
//
// The first already resolved: [rebuildConstructor] sees an
// [hclsyntax.ObjectConsExpr] and rebuilds it leaf by leaf, substituting an
// unknown for the one member that reads a live subnet and keeping the literal
// siblings. The second and third refused, because the argument expression is
// a bare traversal - the constructor is one syntactic step away, inside the
// local, and the module output has no constructor to be a leaf of at all,
// even though the identical reference already resolved as a leaf of one
// ([elementOrUnknown]'s moduleOutput arm).
//
// The child's `count = var.base_configuration["enabled"] ? 1 : 0` is the
// shape that made this matter and is why the fixture is built around a count
// rather than an identity argument. A count needs a whole VALUE;
// [resolver.selectStatic]'s symbolic chase, which is what already reads
// `var.base_configuration["label"]` one argument at a time, cannot answer
// one. So a module-call argument that refuses in one piece refuses every
// instance the child would have had, and none of its resources is resolved
// at all.
//
// The values asserted are the point. "hoisted-0" can only be spelled by
// reading the caller's own local; "netout-0" only by evaluating the child
// module's output expression. A defaulted or fabricated answer spells
// neither.
func TestModuleArgumentSpellingIsNotIdentity(t *testing.T) {
	resolved := resolveModuleArgSpelling(t)

	for _, tc := range []struct{ addr, want string }{
		// The control: written out at the call. Unchanged by this fix, and
		// the answer the other two have to match.
		{"module.inline.aws_iam_role.gated[0]", "hoisted-0"},
		// The same object, hoisted into a local.
		{"module.hoisted.aws_iam_role.gated[0]", "hoisted-0"},
		// A child module's whole output, named on its own.
		{"module.output.aws_iam_role.gated[0]", "netout-0"},
		// merge() of two literal objects, one of whose members reads a
		// live subnet. This one moved with the tolerant static scope
		// ([configs.StaticEvaluator.WithUnknownForRefusedReferences]): the
		// call is still never REBUILT, it is RUN, on a value whose one
		// refused leaf the scope substituted an unknown for, and merge's
		// own answer to that is the one taken. "merged-0" is spelled only
		// by reading the caller's own local through the function the
		// caller wrote.
		{"module.merged.aws_iam_role.gated[0]", "merged-0"},
		// And the member of that output which is a literal in the child:
		// real, so it resolves, where the caller's live subnet does not.
		{"module.output.aws_iam_role.derived[0]", "derived-subnet-from-output"},
	} {
		got, ok := resolved[tc.addr]
		if !ok {
			t.Errorf("%s did not resolve; want %q", tc.addr, tc.want)
			continue
		}
		if got.id != tc.want {
			t.Errorf("%s = %q, want %q", tc.addr, got.id, tc.want)
		}
		if got.class != ClassConcrete {
			t.Errorf("%s class = %s, want CONCRETE", tc.addr, got.class)
		}
	}

	// The equivalence itself, asserted as one fact rather than inferred from
	// two lists: hoisting a constructor into a local changes nothing about
	// what the child resolves, on the resource that resolves AND on the one
	// that does not.
	for _, addr := range []string{"aws_iam_role.gated[0]", "aws_iam_role.derived[0]"} {
		inline, inlineOK := resolved["module.inline."+addr]
		hoisted, hoistedOK := resolved["module.hoisted."+addr]
		if inlineOK != hoistedOK || inline != hoisted {
			t.Errorf("%s: inline resolved to %+v (present=%v) but the same object via a local resolved to %+v (present=%v); the two spellings must be indistinguishable",
				addr, inline, inlineOK, hoisted, hoistedOK)
		}
	}
}

// TestModuleArgumentSpellingRefuses is the adversarial half, and the one that
// decides whether the resolving half is safe to have.
//
// Each case names a spelling whose value this package must NOT produce, and
// asserts the instance does not resolve - never merely that a diagnostic
// exists somewhere. A refusal turned into a wrong marker is worse than the
// refusal, so "did not resolve" is the assertion.
func TestModuleArgumentSpellingRefuses(t *testing.T) {
	resolved := resolveModuleArgSpelling(t)

	for _, tc := range []struct{ addr, why string }{
		{"module.merged.aws_iam_role.derived[0]",
			"the name reads the one member of the merged local that is a live subnet ID; running merge() on a substituted leaf must leave that member unknown, not fill it in"},
		{"module.secret.aws_iam_role.gated[0]",
			"the child module's output is declared sensitive and a marker is written into a cloud tag in clear"},
		{"module.dynamic.aws_iam_role.gated[0]",
			"every member of the local reads a live subnet, so the count itself is unknowable"},
		{"module.hoisted.aws_iam_role.derived[0]",
			"the name reads the one member of the hoisted local that is a live subnet ID; the substituted leaf must not become a marker"},
		{"module.inline.aws_iam_role.derived[0]",
			"the same, written out at the call: the control for the line above"},
	} {
		got, ok := resolved[tc.addr]
		if !ok {
			continue
		}
		if got.id != "" {
			t.Errorf("%s rendered the identity %q but must not: %s", tc.addr, got.id, tc.why)
		}
	}
}

// TestMergeOfBareModuleOutputResolves pins the shape issue #375 named as its
// root cause, and records that it was not the blocker.
//
// `merge({ ... }, module.network.configuration)` in a local, handed on to a
// child module as one bare local reference, is exactly
// backend_modules/aws/base/main.tf's own local.configuration_output. It
// resolved before #375's work and still does, because
// [resolver.selectStatic] reads a step into a merge() of object constructors
// and chases a module-output reference into the child module - one identity
// argument at a time, which is all an identity argument needs.
//
// The asserted value is the one that says the chase really happened:
// "demo" and "eu-west-1a" are written only inside module.network's own output
// expression, two module calls away from the resource that spells them.
func TestMergeOfBareModuleOutputResolves(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "merge-bare-module-output"), nil)
	result, _ := ResolveWith(context.Background(), cfg, Context{})

	resolved := map[string]moduleArgResolution{}
	if result != nil {
		for _, r := range result.All() {
			resolved[r.Addr.String()] = moduleArgResolution{class: r.Class, id: r.ImportID}
		}
	}

	const addr = "module.base.module.host.aws_iam_role.host[0]"
	got, ok := resolved[addr]
	if !ok {
		t.Fatalf("%s did not resolve; the merge()-of-a-bare-module-output shape is expected to resolve here", addr)
	}
	if got.id != "demo-eu-west-1a-0" || got.class != ClassConcrete {
		t.Errorf("%s = %q (%s), want %q (CONCRETE)", addr, got.id, got.class, "demo-eu-west-1a-0")
	}

	// The control in the same module: its name reads the one member of the
	// merged map that is a live subnet ID, and must render nothing.
	if poisoned, ok := resolved["module.base.module.host.aws_iam_role.poisoned[0]"]; ok && poisoned.id != "" {
		t.Errorf("aws_iam_role.poisoned[0] rendered %q but reads a live subnet ID and must render nothing", poisoned.id)
	}
}

type moduleArgResolution struct {
	class Class
	id    string
}

func resolveModuleArgSpelling(t *testing.T) map[string]moduleArgResolution {
	t.Helper()

	cfg := loadConfigTree(t, filepath.Join("testdata", "module-arg-hoisted"), nil)
	result, _ := ResolveWith(context.Background(), cfg, Context{})

	resolved := map[string]moduleArgResolution{}
	if result != nil {
		for _, r := range result.All() {
			resolved[r.Addr.String()] = moduleArgResolution{class: r.Class, id: r.ImportID}
		}
	}
	return resolved
}
