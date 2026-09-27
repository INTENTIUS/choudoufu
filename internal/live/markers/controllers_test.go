// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import "testing"

// TestControllerHeld pins what counts (GitHub issue #1606): any key of
// ControllerTagKeys, and nothing else. The near misses are the ones a
// reader could plausibly mistake for a controller's claim.
func TestControllerHeld(t *testing.T) {
	for name, tc := range map[string]struct {
		tags map[string]string
		want Controller
		held bool
	}{
		"ack default tags":                          {map[string]string{"services.k8s.aws/controller-version": "s3-v1.0.14", "services.k8s.aws/namespace": "team-a"}, ControllerACK, true},
		"ack namespace only":                        {map[string]string{"services.k8s.aws/namespace": "team-a"}, ControllerACK, true},
		"crossplane upjet tags":                     {map[string]string{"crossplane-kind": "bucket.s3.aws.upbound.io", "crossplane-name": "a", "crossplane-providerconfig": "default"}, ControllerCrossplane, true},
		"empty value still counts":                  {map[string]string{"crossplane-name": ""}, ControllerCrossplane, true},
		"estate markers only":                       {map[string]string{TagEstate: "prod", TagAddress: "aws_s3_bucket.a"}, "", false},
		"external-name is an annotation, not a tag": {map[string]string{"crossplane.io/external-name": "a"}, "", false},
		"managed-by is not ACK's own claim":         {map[string]string{"app.kubernetes.io/managed-by": "Helm"}, "", false},
		"nil":                                       {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			hold, ok := ControllerHeld(tc.tags)
			if ok != tc.held || hold.Controller != tc.want {
				t.Errorf("ControllerHeld(%v) = %q, %v; want %q, %v", tc.tags, hold.Controller, ok, tc.want, tc.held)
			}
			if ok && hold.Describe() == "" {
				t.Error("a held resource has no description")
			}
		})
	}
}

// TestControllerTagKeysAreUnique: one list, one entry per key.
func TestControllerTagKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ControllerTagKeys {
		if seen[k.Key] {
			t.Errorf("%s is listed twice", k.Key)
		}
		seen[k.Key] = true
		if k.Controller == "" || k.Names == "" {
			t.Errorf("%s names no controller or no field", k.Key)
		}
	}
}
