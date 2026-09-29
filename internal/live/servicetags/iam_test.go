// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
)

// fakeIAM answers the five operations [IAMRoutes] uses, from a script the
// test writes, and records what it was asked. The wire shape is the SDK's
// own, so what is faked is the service and not the client.
type fakeIAM struct {
	profilePages []*iam.ListInstanceProfileTagsOutput
	mfaPages     []*iam.ListMFADeviceTagsOutput
	policyPages  []*iam.ListPolicyTagsOutput
	rolePages    []*iam.ListRoleTagsOutput
	userPages    []*iam.ListUserTagsOutput
	err          error

	profileNames []string
	serials      []string
	policyARNs   []string
	roleNames    []string
	userNames    []string
	markers      []string

	// listPages and listInputs are [IAMListRoutes]'s half (GitHub issue
	// #1477): the ListRoles pages to serve, in order, and every input the
	// fake was called with, so a test can assert the PathPrefix and the
	// continuation marker each call carried.
	listPages  []*iam.ListRolesOutput
	listInputs []*iam.ListRolesInput
	listErr    error
}

func (f *fakeIAM) ListRoles(_ context.Context, in *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	f.listInputs = append(f.listInputs, in)
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := f.listPages[0]
	f.listPages = f.listPages[1:]
	return out, nil
}

func (f *fakeIAM) ListInstanceProfileTags(_ context.Context, in *iam.ListInstanceProfileTagsInput, _ ...func(*iam.Options)) (*iam.ListInstanceProfileTagsOutput, error) {
	f.profileNames = append(f.profileNames, aws.ToString(in.InstanceProfileName))
	f.markers = append(f.markers, aws.ToString(in.Marker))
	if f.err != nil {
		return nil, f.err
	}
	out := f.profilePages[0]
	f.profilePages = f.profilePages[1:]
	return out, nil
}

func (f *fakeIAM) ListMFADeviceTags(_ context.Context, in *iam.ListMFADeviceTagsInput, _ ...func(*iam.Options)) (*iam.ListMFADeviceTagsOutput, error) {
	f.serials = append(f.serials, aws.ToString(in.SerialNumber))
	f.markers = append(f.markers, aws.ToString(in.Marker))
	if f.err != nil {
		return nil, f.err
	}
	out := f.mfaPages[0]
	f.mfaPages = f.mfaPages[1:]
	return out, nil
}

func (f *fakeIAM) ListPolicyTags(_ context.Context, in *iam.ListPolicyTagsInput, _ ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error) {
	f.policyARNs = append(f.policyARNs, aws.ToString(in.PolicyArn))
	f.markers = append(f.markers, aws.ToString(in.Marker))
	if f.err != nil {
		return nil, f.err
	}
	out := f.policyPages[0]
	f.policyPages = f.policyPages[1:]
	return out, nil
}

func (f *fakeIAM) ListRoleTags(_ context.Context, in *iam.ListRoleTagsInput, _ ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	f.roleNames = append(f.roleNames, aws.ToString(in.RoleName))
	f.markers = append(f.markers, aws.ToString(in.Marker))
	if f.err != nil {
		return nil, f.err
	}
	out := f.rolePages[0]
	f.rolePages = f.rolePages[1:]
	return out, nil
}

func (f *fakeIAM) ListUserTags(_ context.Context, in *iam.ListUserTagsInput, _ ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	f.userNames = append(f.userNames, aws.ToString(in.UserName))
	f.markers = append(f.markers, aws.ToString(in.Marker))
	if f.err != nil {
		return nil, f.err
	}
	out := f.userPages[0]
	f.userPages = f.userPages[1:]
	return out, nil
}

func tag(k, v string) iamtypes.Tag { return iamtypes.Tag{Key: aws.String(k), Value: aws.String(v)} }

// TestIAMReadsTheNativeArmsMarkers is #1125's half of the table: the three
// types the provider's own list resource enumerates and whose IAM list
// operation drops tags by design. Each asserts the identifier reaches the
// right input field, because a role name in a PolicyArn slot would fail
// silently as "no tags" against a real endpoint.
func TestIAMReadsTheNativeArmsMarkers(t *testing.T) {
	t.Run("aws_iam_role", func(t *testing.T) {
		api := &fakeIAM{rolePages: []*iam.ListRoleTagsOutput{{
			Tags: []iamtypes.Tag{
				tag("tofu-estate", "ec2complete"),
				tag("tofu-address", "module.ec2_complete.aws_iam_role.this:0"),
			},
		}}}
		got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_role", "ex-complete")
		if err != nil {
			t.Fatalf("ReadTags: %v", err)
		}
		if got["tofu-estate"] != "ec2complete" || got["tofu-address"] != "module.ec2_complete.aws_iam_role.this:0" {
			t.Fatalf("read %v, want both markers", got)
		}
		if len(api.roleNames) != 1 || api.roleNames[0] != "ex-complete" {
			t.Fatalf("ListRoleTags was asked for RoleName %v, want exactly [ex-complete]", api.roleNames)
		}
	})

	t.Run("aws_iam_policy", func(t *testing.T) {
		const arn = "arn:aws:iam::123456789012:policy/ex-complete"
		api := &fakeIAM{policyPages: []*iam.ListPolicyTagsOutput{{
			Tags: []iamtypes.Tag{tag("tofu-estate", "ec2complete")},
		}}}
		got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_policy", arn)
		if err != nil {
			t.Fatalf("ReadTags: %v", err)
		}
		if got["tofu-estate"] != "ec2complete" {
			t.Fatalf("read %v, want tofu-estate", got)
		}
		if len(api.policyARNs) != 1 || api.policyARNs[0] != arn {
			t.Fatalf("ListPolicyTags was asked for PolicyArn %v, want exactly [%s]", api.policyARNs, arn)
		}
	})

	t.Run("aws_iam_user", func(t *testing.T) {
		api := &fakeIAM{userPages: []*iam.ListUserTagsOutput{{
			Tags: []iamtypes.Tag{tag("tofu-estate", "ec2complete")},
		}}}
		got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_user", "ex-user")
		if err != nil {
			t.Fatalf("ReadTags: %v", err)
		}
		if got["tofu-estate"] != "ec2complete" {
			t.Fatalf("read %v, want tofu-estate", got)
		}
		if len(api.userNames) != 1 || api.userNames[0] != "ex-user" {
			t.Fatalf("ListUserTags was asked for UserName %v, want exactly [ex-user]", api.userNames)
		}
	})
}

// TestIAMEmptyTagReadIsAnAnswer pins the distinction discovery's native leg
// relies on: a tag-read API returning no tags is a successful read of an
// object that carries none, not a failure. ReadTags must return an empty map
// and no error, because the caller turns "ok" into markerReadWorked.
func TestIAMEmptyTagReadIsAnAnswer(t *testing.T) {
	api := &fakeIAM{rolePages: []*iam.ListRoleTagsOutput{{}}}
	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_role", "someone-elses-role")
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("read %v, want an empty map", got)
	}
}

// TestIAMReadsAnInstanceProfilesMarker is the one call #881 turns on: the
// marker Cloud Control cannot carry, read off the object by name.
func TestIAMReadsAnInstanceProfilesMarker(t *testing.T) {
	api := &fakeIAM{profilePages: []*iam.ListInstanceProfileTagsOutput{{
		Tags: []iamtypes.Tag{
			tag("tofu-estate", "terralith"),
			tag("tofu-address", "aws_iam_instance_profile.team_0002_profile"),
			tag("Name", "tl-team-0002-profile"),
		},
	}}}

	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", "tl-team-0002-profile")
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if got["tofu-estate"] != "terralith" || got["tofu-address"] != "aws_iam_instance_profile.team_0002_profile" {
		t.Fatalf("tags = %v, want both markers", got)
	}
	if len(api.profileNames) != 1 || api.profileNames[0] != "tl-team-0002-profile" {
		t.Fatalf("iam:ListInstanceProfileTags was asked for %v, want exactly [tl-team-0002-profile] - the import identity of an instance profile IS its name, and sending anything else would read another object's tags", api.profileNames)
	}
}

// TestIAMReadsAVirtualMFADevicesMarkerBySerial pins the other entry, and the
// mapping that is not the obvious one: the identifier is the ARN and the
// input field is SerialNumber, because IAM documents them as the same string
// for a virtual device.
func TestIAMReadsAVirtualMFADevicesMarkerBySerial(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:mfa/estate-device"
	api := &fakeIAM{mfaPages: []*iam.ListMFADeviceTagsOutput{{
		Tags: []iamtypes.Tag{tag("tofu-estate", "terralith")},
	}}}

	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_virtual_mfa_device", arn)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if got["tofu-estate"] != "terralith" {
		t.Fatalf("tags = %v, want tofu-estate", got)
	}
	if len(api.serials) != 1 || api.serials[0] != arn {
		t.Fatalf("iam:ListMFADeviceTags was asked for %v, want exactly [%s]", api.serials, arn)
	}
}

// TestIAMReadTagsPaginates covers the truncated answer. IAM caps a resource
// at 50 tags against a 100-item default page, so this cannot arise today;
// the loop exists so that the function is right if the cap moves, and this
// test is what says the loop works rather than merely compiles.
func TestIAMReadTagsPaginates(t *testing.T) {
	api := &fakeIAM{profilePages: []*iam.ListInstanceProfileTagsOutput{
		{Tags: []iamtypes.Tag{tag("a", "1")}, IsTruncated: true, Marker: aws.String("page2")},
		{Tags: []iamtypes.Tag{tag("tofu-estate", "terralith")}},
	}}

	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", "p")
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if got["a"] != "1" || got["tofu-estate"] != "terralith" {
		t.Fatalf("tags = %v, want both pages merged - a marker on the second page is a marker that would otherwise be dropped", got)
	}
	if len(api.markers) != 2 || api.markers[0] != "" || api.markers[1] != "page2" {
		t.Fatalf("markers sent = %v, want [\"\" \"page2\"]", api.markers)
	}
}

// TestIAMReadTagsStopsOnARepeatedMarker is the loop's own safety net: a
// service that returns IsTruncated with the marker it was handed would spin
// forever, and a plan that never returns is worse than one that refuses.
func TestIAMReadTagsStopsOnARepeatedMarker(t *testing.T) {
	api := &fakeIAM{profilePages: []*iam.ListInstanceProfileTagsOutput{
		{Tags: []iamtypes.Tag{tag("a", "1")}, IsTruncated: true, Marker: aws.String("stuck")},
		{Tags: []iamtypes.Tag{tag("b", "2")}, IsTruncated: true, Marker: aws.String("stuck")},
	}}

	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", "p")
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("tags = %v, want the two pages it read before the marker repeated", got)
	}
}

// TestIAMReadTagsErrorsAreNotEmptyTagSets is the safety rule this package's
// interface comment states: a failed read must not come back looking like an
// object that carries no marker.
func TestIAMReadTagsErrorsAreNotEmptyTagSets(t *testing.T) {
	api := &fakeIAM{err: errors.New("AccessDenied")}
	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", "p")
	if err == nil {
		t.Fatalf("ReadTags returned tags %v and no error for a failing service - the caller would read that as \"this object carries no marker\", which is the ours/not-ours collapse live/MARKERS.md forbids", got)
	}
	if got != nil {
		t.Errorf("tags = %v, want nil beside the error", got)
	}
}

// TestIAMHasNoRouteForAnUnwiredType, and it is an error rather than an empty
// map for the same reason.
//
// The type is aws_iam_role_policy_attachment, and it is the right one to
// stand here rather than an arbitrary miss: it is the OTHER address #1125's
// corpus-ec2-instance-complete reproduction found undestroyed, and it is
// unwired permanently. IAM has no TagRolePolicyAttachment and no
// ListRolePolicyAttachmentTags - an attachment is not a taggable object -
// so its recovery is the record rung and parent derivation, never this leg.
// Before #1125 this test used aws_iam_role, which now has a route.
func TestIAMHasNoRouteForAnUnwiredType(t *testing.T) {
	const unwired = "aws_iam_role_policy_attachment"
	r := NewIAM(&fakeIAM{})
	if r.Route(unwired) {
		t.Errorf("Route says yes for %s, which carries no tags at all and can never be tag-read", unwired)
	}
	_, err := r.ReadTags(context.Background(), unwired, "ex-complete/arn:aws:iam::aws:policy/AdministratorAccess")
	if !ErrNoRoute(err) {
		t.Fatalf("ReadTags for an unrouted type returned %v, want the no-route error", err)
	}
}

// TestIAMReadTagsRefusesAnEmptyIdentifier: sending an empty name to
// iam:ListInstanceProfileTags is a call that can only fail, and an empty
// identifier is a fact about the listing rather than about the object.
func TestIAMReadTagsRefusesAnEmptyIdentifier(t *testing.T) {
	api := &fakeIAM{}
	if _, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", ""); err == nil {
		t.Fatal("ReadTags accepted an empty identifier")
	}
	if len(api.profileNames) != 0 {
		t.Errorf("a call went out anyway, for %v", api.profileNames)
	}
}

// TestIAMTagsWithNoKeyAreSkipped: a tag with a nil Key cannot be indexed and
// must not panic. The SDK models Key as a pointer, so this is reachable
// from a malformed answer rather than only from a fake.
func TestIAMTagsWithNoKeyAreSkipped(t *testing.T) {
	api := &fakeIAM{profilePages: []*iam.ListInstanceProfileTagsOutput{{
		Tags: []iamtypes.Tag{{Key: nil, Value: aws.String("x")}, tag("tofu-estate", "terralith")},
	}}}
	got, err := NewIAM(api).ReadTags(context.Background(), "aws_iam_instance_profile", "p")
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if len(got) != 1 || got["tofu-estate"] != "terralith" {
		t.Fatalf("tags = %v, want just the one well-formed tag", got)
	}
}

// TestEveryRouteNamesItsAction: the action is what a refused read's gap
// tells the operator to grant (#1162), so a route without one would render
// "Grant , or retry". It is asserted in the form a policy statement uses.
func TestEveryRouteNamesItsAction(t *testing.T) {
	r := NewIAM(&fakeIAM{})
	for typeName, route := range IAMRoutes {
		if !strings.HasPrefix(route.action, "iam:List") || !strings.HasSuffix(route.action, "Tags") {
			t.Errorf("%s's route action is %q, want an iam:List*Tags action", typeName, route.action)
		}
		if got := r.Action(typeName); got != route.action {
			t.Errorf("Action(%s) = %q, want the route's %q", typeName, got, route.action)
		}
	}
	if got := r.Action("aws_iam_role_policy_attachment"); got != "" {
		t.Errorf("Action for an unrouted type = %q, want empty", got)
	}
}

type fakeAPIError struct{ code, msg string }

func (e fakeAPIError) Error() string                 { return e.code + ": " + e.msg }
func (e fakeAPIError) ErrorCode() string             { return e.code }
func (e fakeAPIError) ErrorMessage() string          { return e.msg }
func (e fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

// TestErrorCodeReadsTheSDKCodeFirst: an SDK error carries its code as
// smithy.APIError; anything else is read off the message's leading token.
func TestErrorCodeReadsTheSDKCodeFirst(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"smithy":             {fmt.Errorf("operation error IAM: ListRoleTags, %w", fakeAPIError{"AccessDenied", "not authorized"}), "AccessDenied"},
		"token before colon": {errors.New("Throttling: Rate exceeded"), "Throttling"},
		"prose":              {errors.New("dial tcp: connection refused"), "dial tcp: connection refused"},
		"no colon":           {errors.New("context deadline exceeded"), "context deadline exceeded"},
	}
	for name, c := range cases {
		if got := ErrorCode(c.err); got != c.want {
			t.Errorf("%s: ErrorCode = %q, want %q", name, got, c.want)
		}
	}
}
