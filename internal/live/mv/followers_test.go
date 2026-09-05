// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// followersFixture is the carve-by-retag smoke's own shape
// (live/smoke/scenarios/carve-by-retag.sh, tools/terralith-gen/gen.go's
// writeTeam), reduced to the pieces followersOf's own doc comment argues
// about: a role whose "name" is a plain literal, an inline policy and an
// attachment whose "role" argument is a BARE reference to it (the shape
// identity.Resolve statically folds to ClassConcrete once the role's own
// name is known - the exact fold followersOf is built to see past), a
// second attachment whose OTHER argument needs a live read
// (ClassParentDerived, parented on the policy rather than the role) so it
// still has to be recognised as the role's own follower too, an
// independent policy that is not a child of anything, and an instance
// profile that also reads the role's name but is never a follower because
// its OWN identity is self-sufficient (live/MARKERS.md's admitted table
// gives it no "role" identity component at all).
func followersFixture(t *testing.T) *fixtureConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
resource "aws_iam_role" "team_role" {
  name               = "tl-team-role"
  assume_role_policy = "{}"
}

resource "aws_iam_role_policy" "team_inline" {
  name   = "tl-team-inline"
  role   = aws_iam_role.team_role.name
  policy = "{}"
}

resource "aws_iam_policy" "team_policy" {
  name   = "tl-team-policy"
  policy = "{}"
}

resource "aws_iam_role_policy_attachment" "team_managed_attach" {
  role       = aws_iam_role.team_role.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy_attachment" "team_custom_attach" {
  role       = aws_iam_role.team_role.name
  policy_arn = aws_iam_policy.team_policy.arn
}

resource "aws_iam_instance_profile" "team_profile" {
  name = "tl-team-profile"
  role = aws_iam_role.team_role.name
}

resource "aws_iam_role" "other_role" {
  name               = "tl-other-role"
  assume_role_policy = "{}"
}

resource "aws_iam_role_policy" "other_inline" {
  name   = "tl-other-inline"
  role   = aws_iam_role.other_role.name
  policy = "{}"
}
`)
	cfg := loadConfigDir(t, dir)
	result, diags := identity.Resolve(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity.Resolve on the followers fixture: %s", diags.Err())
	}
	return &fixtureConfig{cfg: cfg, resolutions: result.All()}
}

type fixtureConfig struct {
	cfg         *configs.Config
	resolutions []identity.Resolution
}

// TestFollowersOf is the real-configuration proof: followersOf reads past
// identity.Resolve's own static fold (see followersFixture's doc comment
// for exactly what that means and why a Formula.Parents-only
// implementation missed it - the regression this test pins, caught by the
// carve-by-retag smoke run rather than by a unit test the first time).
func TestFollowersOf(t *testing.T) {
	fx := followersFixture(t)

	role := mustAddr(t, "aws_iam_role.team_role")
	inline := mustAddr(t, "aws_iam_role_policy.team_inline")
	managedAttach := mustAddr(t, "aws_iam_role_policy_attachment.team_managed_attach")
	customAttach := mustAddr(t, "aws_iam_role_policy_attachment.team_custom_attach")

	got := followersOf(fx.cfg, role, fx.resolutions)
	want := []string{inline.String(), managedAttach.String(), customAttach.String()}
	if len(got) != len(want) {
		t.Fatalf("followersOf(role, ...) = %v, want exactly %v", got, want)
	}
	// followersOf sorts by address; aws_iam_role_policy sorts before
	// aws_iam_role_policy_attachment, and the two attachments sort by name.
	gotAddrs := make([]string, len(got))
	for i, f := range got {
		gotAddrs[i] = f.Addr.String()
	}
	for i, w := range []string{inline.String(), customAttach.String(), managedAttach.String()} {
		if gotAddrs[i] != w {
			t.Errorf("followers[%d] = %s, want %s (got order %v)", i, gotAddrs[i], w, gotAddrs)
		}
	}
	for _, f := range got {
		if f.Addr.String() == customAttach.String() && f.TypeName != "aws_iam_role_policy_attachment" {
			t.Errorf("customAttach follower TypeName = %q, want aws_iam_role_policy_attachment", f.TypeName)
		}
	}

	// aws_iam_instance_profile also reads the role's name, but its OWN
	// identity is self-sufficient - never a follower.
	for _, f := range got {
		if f.TypeName == "aws_iam_instance_profile" {
			t.Errorf("aws_iam_instance_profile showed up as a follower of the role: %+v", f)
		}
	}

	// The independent policy and the unrelated role's own inline policy
	// never show up as the role's followers.
	otherRole := mustAddr(t, "aws_iam_role.other_role")
	otherInline := mustAddr(t, "aws_iam_role_policy.other_inline")
	gotOther := followersOf(fx.cfg, otherRole, fx.resolutions)
	if len(gotOther) != 1 || gotOther[0].Addr.String() != otherInline.String() {
		t.Errorf("followersOf(otherRole, ...) = %v, want only %s", gotOther, otherInline)
	}

	if got := followersOf(fx.cfg, mustAddr(t, "aws_iam_role.nobody"), fx.resolutions); len(got) != 0 {
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
// assembled exactly as identity.Resolve itself would (this test reuses
// followersFixture's real resolve pass rather than hand-building
// resolutions, precisely because hand-building them is what let the
// original, wrong implementation of followersOf look correct), Move
// computes res.Anchor and res.Followers before it ever reaches
// req.Providers - so a Request whose provider can never be configured
// still reports the followers a reader needs to draw the move, on the same
// refused Result a caller's -json flag renders.
func TestMoveSetsFollowersBeforeAskingForAProvider(t *testing.T) {
	fx := followersFixture(t)
	role := mustAddr(t, "aws_iam_role.team_role")
	inline := mustAddr(t, "aws_iam_role_policy.team_inline")

	req := Request{
		Estate: "followers-test",
		Old:    role,
		New:    role,
		// FromEstate makes this a same-address move, so checkAddresses does
		// not refuse Old == New before anchorAddr and followersOf ever run.
		FromEstate:  "followers-test-source",
		Config:      fx.cfg,
		Resolutions: fx.resolutions,
		Providers:   fakeNoProviders{},
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
	if len(res.Followers) != 3 {
		t.Fatalf("res.Followers = %v, want the inline policy and the two attachments", res.Followers)
	}
	found := false
	for _, f := range res.Followers {
		if f.Addr.String() == inline.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("res.Followers = %v, does not include %s", res.Followers, inline)
	}
}
