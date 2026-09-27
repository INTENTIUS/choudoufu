// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestControllerHeldIsTheAnswerItReplaced (GitHub issue #1706, "no
// behaviour change"): each family's ControllerHeld gives exactly what its
// leg computed before - markers.ControllerHeld and Describe on AWS tags,
// the Helm release annotation on a Kubernetes object - and reads only its
// own field of the evidence, so the dispatch's order cannot decide.
func TestControllerHeldIsTheAnswerItReplaced(t *testing.T) {
	ack := map[string]string{"services.k8s.aws/controller-version": "s3-v1.0.14", "services.k8s.aws/namespace": "team-a"}
	crossplane := map[string]string{"crossplane-kind": "bucket.s3.aws.upbound.io", "crossplane-name": "assets", "crossplane-providerconfig": "default"}
	helm := map[string]string{kubesweep.HelmReleaseNameAnnotation: "web", kubesweep.HelmReleaseNamespaceAnnotation: "smoke-k8s"}
	helmNoNS := map[string]string{kubesweep.HelmReleaseNameAnnotation: " web "}

	for name, tags := range map[string]map[string]string{"ack": ack, "crossplane": crossplane, "plain": {"Name": "x"}, "nil": nil} {
		want, wantOK := markers.ControllerHeld(tags)
		got, ok := ControllerHeld(HoldEvidence{Tags: tags})
		if ok != wantOK || (ok && (got.Controller != string(want.Controller) || got.HeldBy != want.Describe())) {
			t.Errorf("tags %s: got %+v %v, want %s %q %v", name, got, ok, want.Controller, want.Describe(), wantOK)
		}
		if _, held := Kubernetes.ControllerHeld(HoldEvidence{Tags: tags}); held {
			t.Errorf("tags %s: Kubernetes answered from AWS tags", name)
		}
	}
	for name, ann := range map[string]map[string]string{"helm": helm, "helm-no-namespace": helmNoNS, "plain": {"a": "b"}, "nil": nil} {
		rel, wantOK := kubesweep.HelmReleaseOf(ann)
		got, ok := ControllerHeld(HoldEvidence{Annotations: ann})
		if ok != wantOK || (ok && (got.Controller != kubesweep.ControllerHelm || got.HeldBy != rel.String())) {
			t.Errorf("annotations %s: got %+v %v, want Helm %q %v", name, got, ok, rel.String(), wantOK)
		}
		if _, held := AWS.ControllerHeld(HoldEvidence{Annotations: ann}); held {
			t.Errorf("annotations %s: AWS answered from Kubernetes annotations", name)
		}
	}
	if got, _ := ControllerHeld(HoldEvidence{Annotations: helm}); got.HeldBy != "Helm release smoke-k8s/web" {
		t.Errorf("Helm HeldBy = %q, want the wording the leg printed before: Helm release smoke-k8s/web", got.HeldBy)
	}
	for _, s := range All {
		if rec := s.HoldRecognition(); rec.Mechanism == "" || len(rec.Keys) == 0 {
			t.Errorf("%s HoldRecognition = %+v, want a mechanism and the keys it reads", s.Name(), rec)
		}
	}
}
