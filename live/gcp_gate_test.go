// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	gcpassets "github.com/intentius/choudoufu/live/gcp"
)

// The GCP estate fence (#1121) is project-scoped: an IAM condition cannot
// read a label, so granting an estate is an ordinary project IAM binding,
// shipped as live/gcp/estate-grant.sh and inlined verbatim in
// live/MARKERS.md under "Granting a GCP estate", the way the Kubernetes
// grant is (kubernetes_gate_test.go). Nothing else holds the template to
// what the page says about it: that it binds on the project and nowhere
// wider, conditions nothing on a label, and gives the sweep what it needs.
//
// Proving it red: change "gcloud projects" to "gcloud organizations" in
// one line, drop the roles/cloudasset.viewer binding, or edit the inlined
// copy in MARKERS.md by hand; each fails a different check below.

const gcpGrantTemplate = "gcp/estate-grant.sh"

var gcpBinding = regexp.MustCompile(`(?m)^gcloud (\S+) add-iam-policy-binding (\S+) --member=(\S+) --role=(\S+)(.*)$`)

func TestGCPGrantTemplate(t *testing.T) {
	raw, err := os.ReadFile(gcpGrantTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, gcpassets.EstateGrant()) {
		t.Fatalf("live/gcp's embedded grant differs from %s", gcpGrantTemplate)
	}
	text := string(raw)

	var commands []string
	for _, line := range strings.Split(text, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			commands = append(commands, line)
		}
	}
	bindings := gcpBinding.FindAllStringSubmatch(text, -1)
	if len(bindings) == 0 {
		t.Fatalf("%s holds no add-iam-policy-binding line", gcpGrantTemplate)
	}
	if len(commands) != len(bindings)+1 {
		t.Errorf("%s runs %d commands, want the services enable line and %d bindings: %q", gcpGrantTemplate, len(commands), len(bindings), commands)
	}
	if commands[0] != "gcloud services enable cloudasset.googleapis.com --project=PROJECT" {
		t.Errorf("%s does not start by enabling the Cloud Asset API in PROJECT; the sweep needs it there: %q", gcpGrantTemplate, commands[0])
	}

	roles := map[string]bool{}
	for _, b := range bindings {
		scope, resource, member, role, rest := b[1], b[2], b[3], b[4], b[5]
		if scope != "projects" || resource != "PROJECT" {
			t.Errorf("%s binds on %s %s; the fence is the project, and a wider binding holds every estate beneath it", gcpGrantTemplate, scope, resource)
		}
		if member != "PRINCIPAL" {
			t.Errorf("%s binds member %s, want the PRINCIPAL placeholder", gcpGrantTemplate, member)
		}
		if strings.TrimSpace(rest) != "--condition=None" {
			t.Errorf("%s: the %s binding carries %q; an IAM condition cannot read a label, so the grant states it has none", gcpGrantTemplate, role, rest)
		}
		roles[role] = true
	}
	for _, want := range []string{"roles/cloudasset.viewer", "ROLE"} {
		if !roles[want] {
			t.Errorf("%s binds no %s", gcpGrantTemplate, want)
		}
	}
	if strings.Contains(text, "resource.labels") {
		t.Errorf("%s reads resource.labels; IAM conditions cannot, and the docs say the fence is the project", gcpGrantTemplate)
	}

	markers, err := os.ReadFile("MARKERS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markers), "### Granting a GCP estate") {
		t.Fatalf("MARKERS.md has no \"Granting a GCP estate\" section")
	}
	if !strings.Contains(string(markers), "```sh\n"+text+"```") {
		t.Errorf("MARKERS.md does not inline live/%s verbatim in a sh fence; re-copy it (the file is the source, the doc renders it)", gcpGrantTemplate)
	}
}
