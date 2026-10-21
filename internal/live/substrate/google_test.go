// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import "testing"

// TestGoogleControllerHeld: a GCP copy is held by an exact reserved key on
// the asset type that controller creates, never by a goog- prefix.
func TestGoogleControllerHeld(t *testing.T) {
	const (
		instance = "compute.googleapis.com/Instance"
		disk     = "compute.googleapis.com/Disk"
		service  = "run.googleapis.com/Service"
		cluster  = "dataproc.googleapis.com/Cluster"
	)
	for name, tc := range map[string]struct {
		asset      string
		labels     map[string]string
		held       bool
		controller string
	}{
		"gke node":                      {instance, map[string]string{"goog-gke-node": ""}, true, "GKE"},
		"gke volume":                    {disk, map[string]string{"goog-gke-volume": ""}, true, "GKE"},
		"gke cluster name on a disk":    {disk, map[string]string{"goog-k8s-cluster-name": "c"}, true, "GKE"},
		"dataproc vm":                   {instance, map[string]string{"goog-dataproc-cluster-uuid": "u"}, true, "Dataproc"},
		"batch vm":                      {instance, map[string]string{"batch-job-uid": "j"}, true, "Batch"},
		"cloud functions service":       {service, map[string]string{"goog-managed-by": "cloudfunctions"}, true, "cloudfunctions"},
		"attribution alone":             {instance, map[string]string{"goog-terraform-provisioned": "true"}, false, ""},
		"the declared dataproc cluster": {cluster, map[string]string{"goog-dataproc-cluster-name": "c"}, false, ""},
		"an ejected run service":        {service, map[string]string{"goog-managed-by": ""}, false, ""},
		"a gke key on a bucket":         {"storage.googleapis.com/Bucket", map[string]string{"goog-gke-node": ""}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			hold, ok := Google.ControllerHeld(HoldEvidence{Labels: tc.labels, AssetType: tc.asset})
			if ok != tc.held || hold.Controller != tc.controller {
				t.Errorf("ControllerHeld = %+v, %v; want held %v by %q", hold, ok, tc.held, tc.controller)
			}
		})
	}
	if rec := Google.HoldRecognition(); len(rec.Keys) == 0 || rec.Mechanism == "" {
		t.Errorf("HoldRecognition = %+v", rec)
	}
}

// TestGoogleSweepFindsNothingUnadmitted (GitHub issue #2139, item 5): the
// GCP leg plans no removal and cannot see an unlabelled type, so its
// family answers false. Its deleted blocks are the record leg's, which
// RecordsRemoveUnrowed answers for.
func TestGoogleSweepFindsNothingUnadmitted(t *testing.T) {
	for _, name := range []string{"google", "google-beta"} {
		if Sweeps(name) {
			t.Errorf("Sweeps(%q) = true; the GCP leg removes nothing a record does not", name)
		}
		if !RecordsRemoveUnrowed(name) {
			t.Errorf("RecordsRemoveUnrowed(%q) = false; the record leg is where a deleted google block is removed", name)
		}
	}
}
