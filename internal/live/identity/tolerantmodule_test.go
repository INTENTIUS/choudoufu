// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestTolerantModuleOutputResolvesCountsAndNames is the resolving half of the
// tolerant static scope, and every claim in it is a VALUE rather than the
// absence of a diagnostic.
//
// The fixture is uyuni-project/sumaform's AWS backend reduced to its four
// hops (testdata/tolerant-module-output). Before
// [configs.StaticEvaluator.WithUnknownForRefusedReferences], none of
// module.host's five resources resolved at all, because the argument they
// all read - `base_configuration = module.base.configuration` - was refused
// whole: a module output is outside static scope, and the local behind it
// merges a live subnet into a map whose other members are literals.
//
// The three values asserted are each spelled in a different file, which is
// what makes them evidence that the chase really happened rather than that
// something plausible was produced:
//
//   - "sumaform" is written only in the ESTATE's own call to module.base,
//     and reaches the name through a merge inside module.base's local and a
//     merge inside module.host's;
//   - "default" is written only in module.host's own merge base, and
//     survives because the caller's `{ enabled = true }` overrides a
//     different key;
//   - "eu-west-1a" is written only inside module.net's own output
//     expression, two module calls away from the resource that spells it.
//
// aws_iam_role.absent is the count the estate's route53 record has: a
// lookup() for a key the merged map does not carry. Its answer is the
// default, null, and therefore zero instances - but only when the map's KEY
// SET is known, which is exactly what stubbing the whole argument with an
// unknown destroys. "No instances" is asserted as no instances, not as an
// absent error.
func TestTolerantModuleOutputResolvesCountsAndNames(t *testing.T) {
	resolved, diags := resolveTolerantModuleOutput(t)

	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			t.Errorf("unexpected error diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
	}

	for _, tc := range []struct{ addr, want string }{
		{"module.host.aws_iam_role.gated[0]", "sumaform-default-0"},
		{"module.host.aws_iam_role.zoned[0]", "eu-west-1a-0"},
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

	// The count that must come out ZERO. Any instance of it at all is the
	// failure, whatever it resolved to.
	for addr := range resolved {
		if strings.HasPrefix(addr, "module.host.aws_iam_role.absent") {
			t.Errorf("%s exists, but its count reads a key the merged map does not carry and must expand to no instances", addr)
		}
	}
}

// TestTolerantModuleOutputRefusesSubstitutedLeaves is the adversarial half,
// and the one that decides whether the resolving half is safe to have.
//
// Both resources here read a member of the very same map the two above read,
// and both members are the ones the tolerant scope substituted an unknown
// for: one is the estate's own live subnet, the other a live subnet declared
// inside module.base itself. Neither may render an identity. A refusal
// turned into a wrong marker is worse than the refusal, so these assert "did
// not render", never merely that a diagnostic exists somewhere.
//
// The pair is deliberately in the SAME module, under the SAME count, as the
// resolving pair: the split is per-member of one value, not per-resource or
// per-module, and a fix that widened the substitution into a guess would
// show up here as a plausible-looking name and nowhere else.
func TestTolerantModuleOutputRefusesSubstitutedLeaves(t *testing.T) {
	resolved, _ := resolveTolerantModuleOutput(t)

	for _, tc := range []struct{ addr, why string }{
		{"module.host.aws_iam_role.derived[0]",
			"the name reads the estate's live subnet ID, which the tolerant scope substituted an unknown for"},
		{"module.host.aws_iam_role.profiled[0]",
			"the name reads a server-assigned subnet ID declared inside module.base, refused inside that module's own scope"},
	} {
		got, ok := resolved[tc.addr]
		if !ok {
			continue
		}
		if got.id != "" {
			t.Errorf("%s rendered the identity %q but must not: %s", tc.addr, got.id, tc.why)
		}
		if got.class == ClassConcrete {
			t.Errorf("%s is CONCRETE but must not be: %s", tc.addr, tc.why)
		}
	}
}

func resolveTolerantModuleOutput(t *testing.T) (map[string]moduleArgResolution, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadConfigTree(t, filepath.Join("testdata", "tolerant-module-output"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{})

	resolved := map[string]moduleArgResolution{}
	if result != nil {
		for _, r := range result.All() {
			resolved[r.Addr.String()] = moduleArgResolution{class: r.Class, id: r.ImportID}
		}
	}
	return resolved, diags
}
