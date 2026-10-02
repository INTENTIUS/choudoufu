// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1744 items 1 and 2's documentation pin.
//
// The pages that describe the Kubernetes sweep's exclusions said there were
// two, owner references and an all-control-plane managedFields, and credited
// owner references with keeping a StatefulSet's volumeClaimTemplate PVCs
// out. kubesweep.ControllerMade has four signals (Helm's release annotation
// since #1607, content authorship since #1179), and those PVCs carry no
// owner reference at all. The count is read from ControllerMade's own doc
// comment, so a fifth signal there turns this red until the pages say so.
//
// Proving it red: put "Two exclusions" back in OPERATE.md, or drop "Helm"
// from any page below, or change "four signals" in client.go.

const controllerMadeSource = "../internal/live/kubesweep/client.go"

var controllerMadeSignals = regexp.MustCompile(`ControllerMade reports whether .*\n// .*on (\w+) signals`)

var exclusionCount = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six)\s+(?:exclusions|kinds\s+are\s+excluded)\b`)

// controllerMadeDocs are the pages that state the exclusions. Each must name
// the Helm signal, and any count it gives must be ControllerMade's.
var controllerMadeDocs = []string{
	"kubernetes/ADOPT.md",
	"kubernetes/OPERATE.md",
	"MARKERS.md",
	"smoke/claims/no-silent-orphans.md",
	"../site/content/kubernetes/operate.md",
	"../site/data/providers.yaml",
}

func TestControllerMadeExclusionsMatchTheDocs(t *testing.T) {
	src, err := os.ReadFile(controllerMadeSource)
	if err != nil {
		t.Fatal(err)
	}
	m := controllerMadeSignals.FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s: could not find ControllerMade's \"on N signals\" doc line", controllerMadeSource)
	}
	want := strings.ToLower(string(m[1]))
	for _, p := range controllerMadeDocs {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, c := range exclusionCount.FindAllStringSubmatch(text, -1) {
			if strings.ToLower(c[1]) != want {
				t.Errorf("%s says %q; kubesweep.ControllerMade has %s signals", p, c[0], want)
			}
		}
		if !strings.Contains(text, "Helm") {
			t.Errorf("%s states the sweep's exclusions without the Helm release signal (#1607)", p)
		}
	}
	for _, p := range []string{"kubernetes/ADOPT.md", "kubernetes/OPERATE.md", "MARKERS.md", "smoke/claims/no-silent-orphans.md", "kubernetes/PROOF.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, para := range strings.Split(string(b), "\n\n") {
			flat := strings.Join(strings.Fields(para), " ")
			if !strings.Contains(flat, "olumeClaimTemplate") && !strings.Contains(flat, "volume_claim_template") && !strings.Contains(flat, "PVC") {
				continue
			}
			if strings.Contains(flat, "ownerReferences") && !strings.Contains(flat, "no owner reference") && !strings.Contains(flat, "no `ownerReferences`") && !strings.Contains(flat, "carry none") {
				t.Errorf("%s: a paragraph pairs PVCs with ownerReferences without saying they carry none; a volumeClaimTemplate PVC is excluded by managedFields authorship (#1179): %.200s", p, flat)
			}
		}
	}
}
