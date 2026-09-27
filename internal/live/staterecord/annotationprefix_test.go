// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestAnnotationKeysShareOnePrefix pins GitHub issue #1639's choice of key:
// the address annotation every Kubernetes object carries sits under the
// same choudoufu.intentius.io/ prefix the record store's own annotation
// established, so the fork's annotations on a cluster read as one family.
func TestAnnotationKeysShareOnePrefix(t *testing.T) {
	prefix := func(key string) string {
		i := strings.Index(key, "/")
		if i < 0 {
			t.Fatalf("annotation key %q has no prefix", key)
		}
		return key[:i]
	}
	if got, want := prefix(markers.AddressAnnotation), prefix(KubernetesRecordKeyAnnotation); got != want {
		t.Fatalf("markers.AddressAnnotation's prefix is %q, the record store's is %q", got, want)
	}
	if markers.AddressAnnotation != prefix(KubernetesRecordKeyAnnotation)+"/"+markers.TagAddress {
		t.Errorf("markers.AddressAnnotation = %q, want the record store's prefix and the AWS marker's own name, %q", markers.AddressAnnotation, markers.TagAddress)
	}
}
