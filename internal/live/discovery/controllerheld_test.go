// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"
)

// ackTags are the tags an ACK service controller writes by default onto a
// resource it creates from a custom resource, read off the runtime's
// defaultResourceTags (aws-controllers-k8s/runtime pkg/config/config.go) as
// expanded by pkg/runtime/tags.go: the controller's service alias and
// version, and the CR's Kubernetes namespace.
var ackTags = map[string]string{
	"services.k8s.aws/controller-version": "s3-v1.0.14",
	"services.k8s.aws/namespace":          "team-a",
}

// crossplaneTags are what crossplane-runtime's GetExternalTags
// (pkg/resource/resource.go) writes and upjet's Tagger copies into
// spec.forProvider.tags: the managed resource's kind and name, and its
// ProviderConfig.
var crossplaneTags = map[string]string{
	"crossplane-kind":           "bucket.s3.aws.upbound.io",
	"crossplane-name":           "assets",
	"crossplane-providerconfig": "default",
}

func withTags(base map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TestControllerHeldIsNeverAnOrphan (GitHub issue #1606, ruled on #1604): a
// resource an in-cluster controller made from an owned object is
// controller-held. When it carries this estate's markers at an address the
// configuration no longer declares, the sweep must not propose destroying
// it: the controller would recreate it, and the object that owns it is on
// the cluster side.
func TestControllerHeldIsNeverAnOrphan(t *testing.T) {
	for name, ctrl := range map[string]map[string]string{"ack": ackTags, "crossplane": crossplaneTags} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud()
			ownWholeEstate(cloud)
			cloud.listable("aws_cloudwatch_log_group")
			cloud.obj("aws_cloudwatch_log_group", "/estate/controller", withTags(map[string]string{
				TagEstate:  estateName,
				TagAddress: `aws_cloudwatch_log_group.controller`,
			}, ctrl))

			res, diags := discoverFixture(t, cloud, Request{Sweep: true})
			assertNoErrors(t, diags)

			if _, ok := removalsByAddr(res)[`aws_cloudwatch_log_group.controller`]; ok {
				t.Fatalf("a controller-held resource is proposed for destroy:\n%s", res)
			}
			for _, r := range res.Resolutions {
				if r.Addr.String() == `aws_cloudwatch_log_group.controller` {
					t.Errorf("a controller-held resource entered the prior state: %v", r)
				}
			}
			var withheld bool
			for _, o := range res.Orphans {
				if o.ImportID == "/estate/controller" {
					withheld = !o.Removal && strings.Contains(o.Withheld, "controller-held")
				}
			}
			if !withheld {
				t.Errorf("the controller-held orphan does not say why it was withheld:\n%s", res)
			}
			assertControllerHeld(t, res, "/estate/controller")
		})
	}
}

// TestControllerHeldIsNeverUnclaimed: an ACK- or Crossplane-made resource
// carrying no estate marker is not foreign and not an adoption candidate.
// It leaves the unclaimed population entirely, which is the only input the
// foreign classifier and the adoption offer are built from.
func TestControllerHeldIsNeverUnclaimed(t *testing.T) {
	for name, ctrl := range map[string]map[string]string{"ack": ackTags, "crossplane": crossplaneTags} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
			cloud.obj("aws_security_group", "sg-controller", withTags(map[string]string{"Name": "made-by-a-controller"}, ctrl))
			cloud.obj("aws_security_group", "sg-plain", map[string]string{"Name": "plain"})

			res, diags := discoverFixture(t, cloud, Request{CollectUnclaimed: true})
			assertNoErrors(t, diags)

			var plain bool
			for _, u := range res.Unclaimed {
				if u.ImportID == "sg-controller" {
					t.Errorf("a controller-held resource is in the unclaimed population, where it reads as foreign or adoptable:\n%s", res)
				}
				if u.ImportID == "sg-plain" {
					plain = true
				}
			}
			if !plain {
				t.Errorf("the plain unmarked group was lost with the controller-held one:\n%s", res)
			}
			assertControllerHeld(t, res, "sg-controller")
		})
	}
}
func assertControllerHeld(t *testing.T, res *Result, id string) {
	t.Helper()
	for _, c := range res.ControllerHeld {
		if c.ImportID == id {
			if c.HeldBy == "" || c.Controller == "" {
				t.Errorf("controller-held %s has no description", id)
			}
			return
		}
	}
	t.Errorf("%s is not reported as controller-held:\n%s", id, res)
}
