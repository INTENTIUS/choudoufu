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

// GitHub issue #1477: [IAMListRoutes]'s one entry, exercised against the
// fake IAM API. What these pin is the wire contract the discovery leg
// relies on: PathPrefix sent on every page, the continuation marker
// threaded, the ARN and the name both carried out, a role outside the
// reserved path dropped even when the server returned it, and an error
// surfacing as an error rather than as an empty account.

const (
	slrType = "aws_iam_service_linked_role"
	slrARN  = "arn:aws:iam::000000000000:role/aws-service-role/elasticbeanstalk.amazonaws.com/AWSServiceRoleForElasticBeanstalk"
	slrName = "AWSServiceRoleForElasticBeanstalk"
	slrPath = "/aws-service-role/elasticbeanstalk.amazonaws.com/"
)

func slr(arn, name, path string, tags ...iamtypes.Tag) iamtypes.Role {
	return iamtypes.Role{Arn: aws.String(arn), RoleName: aws.String(name), Path: aws.String(path), Tags: tags}
}

func TestIAMListRouteIsTheServiceLinkedRoleAndNothingElse(t *testing.T) {
	r := NewIAM(&fakeIAM{})
	if !r.ListRoute(slrType) {
		t.Fatalf("no list route for %s", slrType)
	}
	if got := r.ListAction(slrType); got != "iam:ListRoles" {
		t.Fatalf("ListAction(%s) = %q, want iam:ListRoles", slrType, got)
	}
	for _, tn := range []string{"aws_iam_role", "aws_iam_instance_profile", "aws_s3_bucket"} {
		if r.ListRoute(tn) {
			t.Errorf("a list route is answered for %s, which another leg enumerates", tn)
		}
		if got := r.ListAction(tn); got != "" {
			t.Errorf("ListAction(%s) = %q, want \"\"", tn, got)
		}
	}
	if _, err := r.List(context.Background(), "aws_iam_role"); !ErrNoRoute(err) {
		t.Fatalf("List(aws_iam_role) err = %v, want the no-route error", err)
	}
}

func TestIAMListPaginatesAndSendsThePathPrefixOnEveryPage(t *testing.T) {
	api := &fakeIAM{listPages: []*iam.ListRolesOutput{
		{Roles: []iamtypes.Role{slr(slrARN, slrName, slrPath)}, IsTruncated: true, Marker: aws.String("page-2")},
		{Roles: []iamtypes.Role{slr("arn:aws:iam::000000000000:role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS", "AWSServiceRoleForECS", "/aws-service-role/ecs.amazonaws.com/")}},
	}}
	got, err := NewIAM(api).List(context.Background(), slrType)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d objects across two pages, want 2: %+v", len(got), got)
	}
	if got[0].ImportID != slrARN || got[0].ReadKey != slrName || got[0].IdentityAttr != "arn" {
		t.Errorf("first object = %+v, want ImportID=%s ReadKey=%s IdentityAttr=arn", got[0], slrARN, slrName)
	}
	if got[0].Tags != nil {
		t.Errorf("a listing that carried no tags produced Tags=%v, want nil so the caller's own gate reads the marker", got[0].Tags)
	}
	if len(api.listInputs) != 2 {
		t.Fatalf("ListRoles was called %d time(s), want 2", len(api.listInputs))
	}
	for i, in := range api.listInputs {
		if aws.ToString(in.PathPrefix) != serviceLinkedRolePathPrefix {
			t.Errorf("call %d sent PathPrefix %q, want %q", i, aws.ToString(in.PathPrefix), serviceLinkedRolePathPrefix)
		}
	}
	if api.listInputs[0].Marker != nil {
		t.Errorf("the first call carried Marker %q, want none", aws.ToString(api.listInputs[0].Marker))
	}
	if aws.ToString(api.listInputs[1].Marker) != "page-2" {
		t.Errorf("the second call carried Marker %q, want page-2", aws.ToString(api.listInputs[1].Marker))
	}
}

func TestIAMListDropsARoleOutsideTheReservedPath(t *testing.T) {
	api := &fakeIAM{listPages: []*iam.ListRolesOutput{{Roles: []iamtypes.Role{
		slr("arn:aws:iam::000000000000:role/team-role", "team-role", "/"),
		slr(slrARN, slrName, slrPath),
		slr("arn:aws:iam::000000000000:role/svc/team-role-2", "team-role-2", "/svc/"),
	}}}}
	got, err := NewIAM(api).List(context.Background(), slrType)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ImportID != slrARN {
		t.Fatalf("listed %+v, want exactly the one role under %s - a server that ignored PathPrefix must not hand ordinary roles to this leg", got, serviceLinkedRolePathPrefix)
	}
}

func TestIAMListCarriesTagsWhenTheListingHasThem(t *testing.T) {
	api := &fakeIAM{listPages: []*iam.ListRolesOutput{{Roles: []iamtypes.Role{
		slr(slrARN, slrName, slrPath, iamtypes.Tag{Key: aws.String("tofu-estate"), Value: aws.String("e")}),
	}}}}
	got, err := NewIAM(api).List(context.Background(), slrType)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tags["tofu-estate"] != "e" {
		t.Fatalf("listed %+v, want the listing's own tofu-estate tag carried through", got)
	}
}

func TestIAMListReturnsTheErrorNotAnEmptyAccount(t *testing.T) {
	api := &fakeIAM{listErr: errors.New("AccessDenied: User is not authorized to perform iam:ListRoles")}
	got, err := NewIAM(api).List(context.Background(), slrType)
	if err == nil {
		t.Fatalf("a failed ListRoles returned %+v and no error", got)
	}
	if got != nil {
		t.Fatalf("a failed ListRoles returned objects %+v beside its error", got)
	}
	if ErrorCode(err) != "AccessDenied" {
		t.Errorf("ErrorCode = %q, want AccessDenied", ErrorCode(err))
	}
}

// TestIAMServiceLinkedRoleTagReadKeysOnTheName pins the half of #1477 that
// lives in [IAMRoutes]: the sixth entry reads iam:ListRoleTags with
// whatever identifier it is handed, and the caller hands it
// [Listed.ReadKey] - the role name - never the ARN.
func TestIAMServiceLinkedRoleTagReadKeysOnTheName(t *testing.T) {
	api := &fakeIAM{rolePages: []*iam.ListRoleTagsOutput{{Tags: []iamtypes.Tag{
		{Key: aws.String("tofu-estate"), Value: aws.String("e")},
		{Key: aws.String("tofu-address"), Value: aws.String("aws_iam_service_linked_role.app")},
	}}}}
	r := NewIAM(api)
	if got := r.Action(slrType); got != "iam:ListRoleTags" {
		t.Fatalf("Action(%s) = %q, want iam:ListRoleTags", slrType, got)
	}
	tags, err := r.ReadTags(context.Background(), slrType, slrName)
	if err != nil {
		t.Fatal(err)
	}
	if tags["tofu-address"] != "aws_iam_service_linked_role.app" {
		t.Fatalf("read %v", tags)
	}
	if len(api.roleNames) != 1 || api.roleNames[0] != slrName {
		t.Fatalf("ListRoleTags was asked for RoleName %v, want [%s]", api.roleNames, slrName)
	}
}
