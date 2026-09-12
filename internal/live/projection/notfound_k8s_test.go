// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1064: hashicorp/kubernetes surfaces apimachinery's own
// NotFound message out of ImportResourceState, and a greenfield apply of
// a type admitted through the object-metadata rule reaches that import
// before the object exists. The shape is matched, not the words.
func TestNotFoundDiagnosticsRecognisesKubernetesNotFound(t *testing.T) {
	notFound := func(summary, detail string) tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		return diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
	}
	for name, tc := range map[string]struct {
		summary, detail string
		want            bool
	}{
		"serviceaccounts":    {"Unable to fetch service account \"smoke-k8s/app\" from Kubernetes", "serviceaccounts \"app\" not found", true},
		"configmaps":         {"configmaps \"app-config\" not found", "", true},
		"crd resource":       {"Cannot import", "deployments.apps \"web\" not found", true},
		"english not found":  {"Provider produced invalid response", "the endpoint was not found in the region", false},
		"credentials":        {"Unauthorized", "serviceaccounts is forbidden: User \"x\" cannot get resource", false},
		"aws sdk convention": {"couldn't find resource", "", true},
	} {
		got, _ := notFoundDiagnostics(notFound(tc.summary, tc.detail))
		if got != tc.want {
			t.Errorf("%s: notFoundDiagnostics = %v, want %v", name, got, tc.want)
		}
	}
}
