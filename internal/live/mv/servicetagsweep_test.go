// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/servicetags"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1274, at unit scale and with no container: live-mv could not
// see an ownership marker that live-plan could, because internal/live/mv's
// sweep had #266's tag-index fallback and not #1125's per-service tag read.
//
// The estate it was found on is corpus-iam-policy, whose day2_rename stage
// reports the contrast in one log: D1, the moved-block half, renames through
// live-plan and passes; D2, the live-mv half, refuses the identical resource
// with "No live resource at the old address" while the AWS CLI reads
// tofu-address off it eight lines earlier. That stage is a two-container run
// of several minutes and it only reached D2 at all once #1271 stopped
// greenfield aborting the script. This file is the same claim in under a
// second, and it is the one that will still be running in a year.
//
// # The seam, and why it is this one
//
// [mover.locateByList] is where the question is actually asked: it sweeps
// the type, keeps the objects carrying this estate's marker, and refuses
// when none carries the old address. Everything above it (find's class
// routing) and below it (materialize's read-back) is unchanged by #1274 and
// covered elsewhere, so putting the guard here keeps it fast and keeps it
// pointed at the thing that broke.
//
// # What stands in for what
//
// The provider is a mock whose list call returns objects with a tags
// attribute and nothing in it - which is not a caricature, it is what
// iam:ListPolicies does, in AWS's own words: "this operation does not
// return tags, even though they are an attribute of the returned object".
//
// The reader is the REAL [servicetags.IAM] over a fake IAM API, not a
// hand-rolled Reader. That matters: a hand-rolled one would answer for any
// type the test cared to name, and would pass just as happily if
// aws_iam_policy were not in [servicetags.IAMRoutes] at all, or if the route
// passed the wrong identifier. Going through the real reader means the test
// consults that table rather than restating it, and the fake API asserts the
// PolicyArn it is handed.
//
// Request.Tagging is nil throughout. That is the pinned emulator's own
// condition for IAM, not a convenience: floci's GetResources indexes no IAM
// at all (lex00/floci#205, #1152) and real AWS indexes only some of it
// (#1134), so on this target the tag index has nothing to say about an
// aws_iam_policy however it is tagged - which is the whole reason a second
// route had to exist.

const (
	// iamSweepEstate is corpus-iam-policy's own estate name, so the
	// fixture and the failing stage name the same thing.
	iamSweepEstate = "iam-policy-crossing"
	iamSweepType   = "aws_iam_policy"

	// iamSweepTargetARN is the policy being renamed and
	// iamSweepOtherARN a second one of the same type in the same estate -
	// two, as D2's log reports ("The provider listed 2 aws_iam_policy"),
	// so that "found the right one" is a real claim rather than "found the
	// only one".
	iamSweepTargetARN = "arn:aws:iam::000000000000:policy/example"
	iamSweepOtherARN  = "arn:aws:iam::000000000000:policy/example_from_data_source"
)

// iamSweepProviderAddr is the provider configuration the fixture resolves
// to, matching internal/live/projection's own awsProvider test constant.
var iamSweepProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// iamSweepSchema is aws_iam_policy reduced to what a sweep touches: the
// identity attribute the type's row names first (arn), and the tag surface
// a marker lives on.
func iamSweepSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"arn":      {Type: cty.String, Computed: true},
				"id":       {Type: cty.String, Computed: true},
				"name":     {Type: cty.String, Optional: true},
				"tags":     {Type: cty.Map(cty.String), Optional: true},
				"tags_all": {Type: cty.Map(cty.String), Computed: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"arn": {Type: cty.String, Required: true},
			},
		},
	}
}

// iamSweepListed is one live policy as the LIST call serves it. tags is
// what the list call carries, which for iam:ListPolicies is nothing - a nil
// map here produces an object whose tags attribute exists and is empty,
// which is the shape that made the marker unreadable.
type iamSweepListed struct {
	arn  string
	tags map[string]string
}

func iamSweepObject(o iamSweepListed) cty.Value {
	tagVals := make(map[string]cty.Value, len(o.tags))
	for k, v := range o.tags {
		tagVals[k] = cty.StringVal(v)
	}
	tags := cty.MapValEmpty(cty.String)
	if len(tagVals) > 0 {
		tags = cty.MapVal(tagVals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"arn":      cty.StringVal(o.arn),
		"id":       cty.StringVal(o.arn),
		"name":     cty.StringVal(o.arn),
		"tags":     tags,
		"tags_all": tags,
	})
}

// iamSweepProvider adds the list protocol, which providers.Interface does
// not declare - the stateless list client asks for it by type assertion.
type iamSweepProvider struct {
	*tofu.MockProvider
	objects []iamSweepListed
}

func (p *iamSweepProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	for _, o := range p.objects {
		ev := providers.ListResourceEvent{
			DisplayName: o.arn,
			Identity:    cty.ObjectVal(map[string]cty.Value{"arn": cty.StringVal(o.arn)}),
		}
		if req.IncludeResourceObject {
			ev.ResourceObject = iamSweepObject(o)
		}
		if !emit(ev) {
			break
		}
	}
	return nil
}

func newIAMSweepProvider(objects ...iamSweepListed) *iamSweepProvider {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{iamSweepType: iamSweepSchema()},
			ListResourceTypes: map[string]providers.Schema{iamSweepType: {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"region": {Type: cty.String, Optional: true},
				},
			}}},
		},
	}
	p.ConfigureProviderCalled = true
	return &iamSweepProvider{MockProvider: p, objects: objects}
}

// iamSweepAPI is the fake behind the real [servicetags.IAM]: it answers
// iam:ListPolicyTags for the ARNs it was given, records every ARN it was
// asked about, and fails the four operations this type has no business
// reaching. errFor makes one ARN's read fail, for the arm that proves a
// failed read leaves the refusal standing.
type iamSweepAPI struct {
	tags   map[string][]iamtypes.Tag
	errFor map[string]error
	reads  []string
}

func (a *iamSweepAPI) ListPolicyTags(_ context.Context, in *iam.ListPolicyTagsInput, _ ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error) {
	arn := aws.ToString(in.PolicyArn)
	a.reads = append(a.reads, arn)
	if err, ok := a.errFor[arn]; ok {
		return nil, err
	}
	return &iam.ListPolicyTagsOutput{Tags: a.tags[arn]}, nil
}

func (a *iamSweepAPI) ListInstanceProfileTags(context.Context, *iam.ListInstanceProfileTagsInput, ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error) {
	return nil, errors.New("a policy sweep must not reach iam:ListInstanceProfileTags")
}

func (a *iamSweepAPI) ListMFADeviceTags(context.Context, *iam.ListMFADeviceTagsInput, ...func(*iam.Options)) (*iam.ListMFADeviceTagsOutput, error) {
	return nil, errors.New("a policy sweep must not reach iam:ListMFADeviceTags")
}

func (a *iamSweepAPI) ListRoleTags(context.Context, *iam.ListRoleTagsInput, ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	return nil, errors.New("a policy sweep must not reach iam:ListRoleTags")
}

func (a *iamSweepAPI) ListUserTags(context.Context, *iam.ListUserTagsInput, ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	return nil, errors.New("a policy sweep must not reach iam:ListUserTags")
}

func (a *iamSweepAPI) ListRoles(context.Context, *iam.ListRolesInput, ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return nil, errors.New("a policy sweep must not reach iam:ListRoles")
}

func iamTag(key, value string) iamtypes.Tag {
	return iamtypes.Tag{Key: aws.String(key), Value: aws.String(value)}
}

// iamSweepMover builds the mover under test and the list schema
// [mover.locateByList] needs. reader may be nil, which is main's behavior
// before #1274 and this file's BREAK arm.
func iamSweepMover(t *testing.T, provider providers.Interface, reader servicetags.Reader) (*mover, listclient.TypeSchema) {
	t.Helper()

	oldAddr := mustAddr(t, "module.iam_policy.aws_iam_policy.policy[0]")
	newAddr := mustAddr(t, "module.iam_policy_renamed2.aws_iam_policy.policy[0]")

	m := &mover{
		req: Request{
			Estate: iamSweepEstate,
			Old:    oldAddr,
			New:    newAddr,
			Resolutions: []identity.Resolution{
				{Addr: newAddr, Class: identity.ClassNeedsDiscovery, Reason: "IAM mints the policy's own ARN at create time"},
			},
			Providers: projection.SingleProvider(iamSweepProviderAddr, provider),
			// Deliberately nil: see this file's header. The tag index is
			// not a route to an IAM marker on this target.
			Tagging:     nil,
			ServiceTags: reader,
		},
		res: &Result{
			Old:       oldAddr,
			New:       newAddr,
			TypeName:  iamSweepType,
			Anchor:    newAddr,
			OldMarker: discovery.EscapeAddress(oldAddr.String()),
			NewMarker: discovery.EscapeAddress(newAddr.String()),
		},
		provider: provider,
		schema:   iamSweepSchema(),
	}

	schemas, diags := listclient.ListSchemas(t.Context(), provider)
	if diags.HasErrors() {
		t.Fatalf("reading the fixture provider's list schemas: %s", diags.Err())
	}
	ts, listable := schemas.Get(iamSweepType)
	if !listable {
		t.Fatalf("the fixture provider does not serve a list schema for %s, so this test would prove nothing", iamSweepType)
	}
	return m, ts
}

// TestSweepReadsAnIAMMarkerThroughTheServiceTagAPI is #1274's decisive
// pair, run rather than asserted: the SAME fixture, differing only in
// whether Request.ServiceTags is wired.
//
// Unwired is main's behavior before this change, and it must still refuse -
// correctly, because with no route to the marker the sweep genuinely cannot
// see one, and #1274's own "Not this" section is explicit that the fix is
// not to loosen the refusal. Wired, the same sweep binds the same object.
func TestSweepReadsAnIAMMarkerThroughTheServiceTagAPI(t *testing.T) {
	ctx := t.Context()

	// Both policies carry this estate's marker on the live object, and the
	// list call returns neither of them - which is the situation D2 hit.
	objects := []iamSweepListed{
		{arn: iamSweepTargetARN},
		{arn: iamSweepOtherARN},
	}
	oldMarker := discovery.EscapeAddress("module.iam_policy.aws_iam_policy.policy[0]")
	otherMarker := discovery.EscapeAddress("module.iam_policy_from_data_source.aws_iam_policy.policy[0]")

	api := &iamSweepAPI{tags: map[string][]iamtypes.Tag{
		iamSweepTargetARN: {
			iamTag(discovery.TagEstate, iamSweepEstate),
			iamTag(discovery.TagAddress, oldMarker),
		},
		iamSweepOtherARN: {
			iamTag(discovery.TagEstate, iamSweepEstate),
			iamTag(discovery.TagAddress, otherMarker),
		},
	}}

	// BREAK arm. Not an assertion about wording for its own sake: this is
	// the exact refusal #1274 quotes from the failing stage, so if it stops
	// firing without the leg the test has stopped measuring the defect.
	t.Run("unwired", func(t *testing.T) {
		m, ts := iamSweepMover(t, newIAMSweepProvider(objects...), nil)
		liveID, _, diags := m.locateByList(ctx, ts)
		if !diags.HasErrors() {
			t.Fatalf("with no service tag reader the sweep bound %q; the marker is unreadable by every route this run has, so it must refuse", liveID)
		}
		if code := RefusalFrom(diags); code != RefusalNothingAtOldAddress {
			t.Errorf("refusal code = %q, want %q", code, RefusalNothingAtOldAddress)
		}
		detail := diags.Err().Error()
		for _, want := range []string{"listed 2 aws_iam_policy", "0 of which carry estate"} {
			if !strings.Contains(detail, want) {
				t.Errorf("the refusal is not #1274's; missing %q:\n%s", want, detail)
			}
		}
	})

	// GREEN arm.
	t.Run("wired", func(t *testing.T) {
		m, ts := iamSweepMover(t, newIAMSweepProvider(objects...), servicetags.NewIAM(api))
		liveID, liveIdentity, diags := m.locateByList(ctx, ts)
		if diags.HasErrors() {
			t.Fatalf("the sweep refused a policy whose marker iam:ListPolicyTags returns: %s", diags.Err())
		}
		if liveID != iamSweepTargetARN {
			t.Errorf("bound live ID %q, want %q", liveID, iamSweepTargetARN)
		}
		if liveIdentity.IsNull() || liveIdentity.GetAttr("arn").AsString() != iamSweepTargetARN {
			t.Errorf("bound identity %#v, want the listed object's own arn", liveIdentity)
		}
		if m.res.LiveID != iamSweepTargetARN {
			t.Errorf("Result.LiveID = %q, want %q", m.res.LiveID, iamSweepTargetARN)
		}
		// The identifier the route was handed has to be the one PolicyArn
		// wants. Asserted because getting it wrong is the failure mode a
		// hand-rolled reader would have hidden: the read would return
		// nothing and the sweep would refuse exactly as it does unwired.
		if len(api.reads) != 2 {
			t.Fatalf("iam:ListPolicyTags was called for %v, want one call per listed policy", api.reads)
		}
		for _, got := range api.reads {
			if !strings.HasPrefix(got, "arn:aws:iam::") {
				t.Errorf("iam:ListPolicyTags was handed %q, which is not an ARN", got)
			}
		}
	})
}

// TestSweepDoesNotReadTheServiceForAnObjectThatAnsweredForItself pins the
// leg's second gate clause at this call site. An object whose own list
// result carried tofu-estate has already told the truth about itself, and
// paying one IAM call per such object would undo #1037/#1039's flat sweep
// for every estate that has any.
//
// It is also the direction a careless fix breaks: reading the service
// unconditionally would let a stale tag read overrule a fresh list.
func TestSweepDoesNotReadTheServiceForAnObjectThatAnsweredForItself(t *testing.T) {
	oldMarker := discovery.EscapeAddress("module.iam_policy.aws_iam_policy.policy[0]")

	// This time the list call DOES carry the tags, as ec2:DescribeVpcs
	// would.
	provider := newIAMSweepProvider(iamSweepListed{
		arn: iamSweepTargetARN,
		tags: map[string]string{
			discovery.TagEstate:  iamSweepEstate,
			discovery.TagAddress: oldMarker,
		},
	})
	api := &iamSweepAPI{}
	m, ts := iamSweepMover(t, provider, servicetags.NewIAM(api))

	liveID, _, diags := m.locateByList(t.Context(), ts)
	if diags.HasErrors() {
		t.Fatalf("the sweep refused an object whose own list result carried the marker: %s", diags.Err())
	}
	if liveID != iamSweepTargetARN {
		t.Errorf("bound live ID %q, want %q", liveID, iamSweepTargetARN)
	}
	if len(api.reads) != 0 {
		t.Errorf("iam:ListPolicyTags was called for %v; an object that answered for itself needs no second opinion", api.reads)
	}
}

// TestSweepKeepsRefusingWhenTheServiceReadFails is the safety direction.
// A tag read that errors establishes nothing - not "this object carries no
// marker" - so the refusal the run already had must stand, unchanged.
// Turning a failed read into an untagged object is how a rename would
// silently pick the wrong live resource, or report that a resource it is
// looking straight at does not exist.
func TestSweepKeepsRefusingWhenTheServiceReadFails(t *testing.T) {
	provider := newIAMSweepProvider(iamSweepListed{arn: iamSweepTargetARN})
	api := &iamSweepAPI{
		errFor: map[string]error{iamSweepTargetARN: errors.New("AccessDenied: iam:ListPolicyTags")},
	}
	m, ts := iamSweepMover(t, provider, servicetags.NewIAM(api))

	liveID, _, diags := m.locateByList(t.Context(), ts)
	if !diags.HasErrors() {
		t.Fatalf("a failed tag read bound %q; nothing was established by that read", liveID)
	}
	if code := RefusalFrom(diags); code != RefusalNothingAtOldAddress {
		t.Errorf("refusal code = %q, want %q - the pre-existing refusal, unchanged", code, RefusalNothingAtOldAddress)
	}
	if len(api.reads) != 1 {
		t.Errorf("iam:ListPolicyTags reads = %v, want exactly one attempt", api.reads)
	}
}

// TestSweepDoesNotAdoptAnotherEstatesPolicyThroughTheServiceRead is the
// "what does this newly accept" question, asked of the leg itself. The
// service read supplies the object's REAL tags and changes nothing about
// what they have to say: a policy carrying another estate's tofu-estate is
// not this estate's, however it was read.
func TestSweepDoesNotAdoptAnotherEstatesPolicyThroughTheServiceRead(t *testing.T) {
	oldMarker := discovery.EscapeAddress("module.iam_policy.aws_iam_policy.policy[0]")

	provider := newIAMSweepProvider(iamSweepListed{arn: iamSweepTargetARN})
	api := &iamSweepAPI{tags: map[string][]iamtypes.Tag{
		iamSweepTargetARN: {
			iamTag(discovery.TagEstate, "some-other-estate"),
			iamTag(discovery.TagAddress, oldMarker),
		},
	}}
	m, ts := iamSweepMover(t, provider, servicetags.NewIAM(api))

	liveID, _, diags := m.locateByList(t.Context(), ts)
	if !diags.HasErrors() {
		t.Fatalf("the sweep bound %q, a policy carrying estate %q; the service read reports tags, it does not confer ownership", liveID, "some-other-estate")
	}
	if code := RefusalFrom(diags); code != RefusalNothingAtOldAddress {
		t.Errorf("refusal code = %q, want %q", code, RefusalNothingAtOldAddress)
	}
}
