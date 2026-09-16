// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// fakeIAM answers the two operations [IAMRoutes] uses, from a script the
// test writes, and records what it was asked. The wire shape is the SDK's
// own, so what is faked is the service and not the client.
type fakeIAM struct {
	profilePages []*iam.ListInstanceProfileTagsOutput
	mfaPages     []*iam.ListMFADeviceTagsOutput
	err          error

	profileNames []string
	serials      []string
	markers      []string
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

func tag(k, v string) iamtypes.Tag { return iamtypes.Tag{Key: aws.String(k), Value: aws.String(v)} }

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
func TestIAMHasNoRouteForAnUnwiredType(t *testing.T) {
	r := NewIAM(&fakeIAM{})
	if r.Route("aws_iam_role") {
		t.Error("Route says yes for aws_iam_role, which reaches the native leg and not this one")
	}
	_, err := r.ReadTags(context.Background(), "aws_iam_role", "some-role")
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
