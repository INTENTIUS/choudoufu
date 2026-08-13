// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestNormalizeServiceID(t *testing.T) {
	cases := map[string]string{
		"EC2":                     "ec2",
		"ApplicationAutoScaling":  "applicationautoscaling",
		"application-autoscaling": "applicationautoscaling",
		"S3":                      "s3",
	}
	for in, want := range cases {
		if got := normalizeServiceID(in); got != want {
			t.Errorf("normalizeServiceID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDirectory_DirectNormalizedMatch(t *testing.T) {
	dirs := map[string]string{"ec2": "2016-11-15", "application-autoscaling": "2016-02-06"}

	if got, ok := resolveDirectory("EC2", dirs); !ok || got != "ec2" {
		t.Errorf("resolveDirectory(EC2) = %q, %v, want ec2, true", got, ok)
	}
	if got, ok := resolveDirectory("ApplicationAutoScaling", dirs); !ok || got != "application-autoscaling" {
		t.Errorf("resolveDirectory(ApplicationAutoScaling) = %q, %v, want application-autoscaling, true", got, ok)
	}
}

func TestResolveDirectory_ManualAlias(t *testing.T) {
	dirs := map[string]string{"acm": "2015-12-08", "elbv2": "2015-12-01"}

	if got, ok := resolveDirectory("CertificateManager", dirs); !ok || got != "acm" {
		t.Errorf("resolveDirectory(CertificateManager) = %q, %v, want acm, true", got, ok)
	}
	if got, ok := resolveDirectory("ElasticLoadBalancingV2", dirs); !ok || got != "elbv2" {
		t.Errorf("resolveDirectory(ElasticLoadBalancingV2) = %q, %v, want elbv2, true", got, ok)
	}
}

// TestResolveDirectory_AliasTargetMissingIsRefused: an alias whose target
// directory the fetched tree does not actually carry is refused rather than
// trusted - the drift guard resolveDirectory's own doc comment promises.
func TestResolveDirectory_AliasTargetMissingIsRefused(t *testing.T) {
	dirs := map[string]string{"ec2": "2016-11-15"} // no "acm" at all
	if _, ok := resolveDirectory("CertificateManager", dirs); ok {
		t.Error("resolveDirectory resolved an alias whose target is absent from the directory set")
	}
}

func TestResolveDirectory_Unresolved(t *testing.T) {
	dirs := map[string]string{"ec2": "2016-11-15"}
	if _, ok := resolveDirectory("SomeServiceNobodyHasHeardOf", dirs); ok {
		t.Error("resolveDirectory resolved a service with no matching directory and no alias")
	}
}
