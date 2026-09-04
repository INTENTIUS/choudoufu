// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestFollowersOf is the pure-function half of GitHub issue #791's
// followers field: [followersOf] over hand-built resolutions, the way
// TestCheckAddresses (checkaddresses_test.go) tests checkAddresses without
// a config tree or a provider - a follower is a fact about the identity
// graph [identity.Resolve] already produced, and this package only walks
// it, so nothing here needs to reproduce how [identity.ClassParentDerived]
// resolutions come to exist in the first place.
func TestFollowersOf(t *testing.T) {
	role := mustAddr(t, "aws_iam_role.team_role")
	otherRole := mustAddr(t, "aws_iam_role.other_role")
	inline := mustAddr(t, "aws_iam_role_policy.team_inline")
	attach := mustAddr(t, "aws_iam_role_policy_attachment.team_attach")
	unrelated := mustAddr(t, "aws_iam_role_policy.other_inline")

	resolutions := []identity.Resolution{
		{Addr: role, Class: identity.ClassConcrete, ImportID: "team-role"},
		{Addr: inline, Class: identity.ClassParentDerived, Formula: &identity.Formula{
			Parents: []addrs.AbsResourceInstance{role},
		}},
		{Addr: attach, Class: identity.ClassParentDerived, Formula: &identity.Formula{
			Parents: []addrs.AbsResourceInstance{role},
		}},
		// A parent-derived instance of a DIFFERENT parent must not show up as
		// a follower of role - the whole point of matching on Formula.Parents
		// rather than "any parent-derived resolution in the configuration".
		{Addr: unrelated, Class: identity.ClassParentDerived, Formula: &identity.Formula{
			Parents: []addrs.AbsResourceInstance{otherRole},
		}},
		// A concrete resolution that happens to reference nothing: never a
		// follower, whatever its address looks like.
		{Addr: otherRole, Class: identity.ClassConcrete, ImportID: "other-role"},
		// A parent-derived resolution with no Formula at all must not panic
		// - defensive, since every real one identity.Resolve produces has
		// one, but a caller-assembled Request (this package's own tests
		// among them) is not obligated to.
		{Addr: mustAddr(t, "aws_iam_role_policy.no_formula"), Class: identity.ClassParentDerived},
	}

	got := followersOf(role, resolutions)
	if len(got) != 2 {
		t.Fatalf("followersOf(role, ...) = %v, want exactly the inline policy and the attachment", got)
	}
	// Sorted by address: aws_iam_role_policy sorts before
	// aws_iam_role_policy_attachment.
	if got[0].Addr.String() != inline.String() || got[0].TypeName != "aws_iam_role_policy" {
		t.Errorf("first follower = %+v, want %s (aws_iam_role_policy)", got[0], inline)
	}
	if got[1].Addr.String() != attach.String() || got[1].TypeName != "aws_iam_role_policy_attachment" {
		t.Errorf("second follower = %+v, want %s (aws_iam_role_policy_attachment)", got[1], attach)
	}

	if got := followersOf(otherRole, resolutions); len(got) != 1 || got[0].Addr.String() != unrelated.String() {
		t.Errorf("followersOf(otherRole, ...) = %v, want only %s", got, unrelated)
	}

	if got := followersOf(mustAddr(t, "aws_iam_role.nobody"), resolutions); len(got) != 0 {
		t.Errorf("followersOf on a parent with no children = %v, want none", got)
	}
}

// fakeNoProviders is a [projection.Providers] that never resolves a
// provider, standing in for a run whose provider configuration is broken or
// absent. It exists so this file's integration test can exercise [Move]'s
// real anchor-and-followers computation without also having to stand up a
// working mock AWS provider the way internal/command/live_mv_test.go's
// mvCloud does - res.Followers is set before [Move] ever asks Providers for
// anything (see [Result.Followers]'s own doc comment), so a Request that
// fails immediately afterward still proves the field landed.
type fakeNoProviders struct{}

func (fakeNoProviders) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return nil, errNoProvider
}

var errNoProvider = &noProviderError{}

type noProviderError struct{}

func (*noProviderError) Error() string { return "no provider configured in this test" }

// TestMoveSetsFollowersBeforeAskingForAProvider is the integration half:
// over a real loaded configuration and a Request whose Resolutions a caller
// assembled by hand (exactly as internal/command/live_mv.go's statelessResolve
// does for a real run, just skipped here), Move computes res.Anchor and
// res.Followers before it ever reaches req.Providers - so a Request whose
// provider can never be configured still reports the followers a reader
// needs to draw the move, on the same refused Result a caller's -json flag
// renders.
func TestMoveSetsFollowersBeforeAskingForAProvider(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
resource "aws_iam_role" "team_role" {
  name = "team-role"
}
`)
	cfg := loadConfigDir(t, dir)

	role := mustAddr(t, "aws_iam_role.team_role")
	inline := mustAddr(t, "aws_iam_role_policy.team_inline")
	attach := mustAddr(t, "aws_iam_role_policy_attachment.team_attach")

	req := Request{
		Estate: "followers-test",
		Old:    role,
		New:    role,
		// FromEstate makes this a same-address move, so checkAddresses does
		// not refuse Old == New before anchorAddr and followersOf ever run.
		FromEstate: "followers-test-source",
		Config:     cfg,
		Resolutions: []identity.Resolution{
			{Addr: role, Class: identity.ClassConcrete, ImportID: "team-role"},
			{Addr: inline, Class: identity.ClassParentDerived, Formula: &identity.Formula{
				Parents: []addrs.AbsResourceInstance{role},
			}},
			{Addr: attach, Class: identity.ClassParentDerived, Formula: &identity.Formula{
				Parents: []addrs.AbsResourceInstance{role},
			}},
		},
		Providers: fakeNoProviders{},
	}

	res, diags := Move(t.Context(), req)
	if !diags.HasErrors() {
		t.Fatalf("Move succeeded with a Providers that never configures one; that should be impossible")
	}
	if res == nil {
		t.Fatalf("Move returned a nil Result even though it got well past its early nil-Providers guard")
	}
	if res.Anchor.String() != role.String() {
		t.Errorf("res.Anchor = %s, want %s", res.Anchor, role)
	}
	if len(res.Followers) != 2 {
		t.Fatalf("res.Followers = %v, want the inline policy and the attachment", res.Followers)
	}
	if res.Followers[0].Addr.String() != inline.String() || res.Followers[1].Addr.String() != attach.String() {
		t.Errorf("res.Followers = %v, want [%s, %s] in address order", res.Followers, inline, attach)
	}
}
