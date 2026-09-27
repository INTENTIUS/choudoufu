// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// TestEveryPostCreateWriteHasAWriter (GitHub issue #1587): every
// post-create write a family in substrate.All declares, other than
// WriteNeverNeeded, has a client this build can make, so no family's
// creates are left unmarked for want of one. Deleting an entry from
// markerWriters fails here by the family's name.
func TestEveryPostCreateWriteHasAWriter(t *testing.T) {
	for _, sub := range substrate.All {
		for _, surface := range sub.Surfaces() {
			write := sub.Writes(surface).PostCreate
			if write == substrate.WriteNeverNeeded {
				continue
			}
			if _, ok := markerWriters[write]; !ok {
				t.Errorf("provider family %s surface %s declares the %q post-create write and no writer serves it", sub.Name(), surface, write)
			}
		}
	}
}

// TestMarkerWriterChosenByTheWrite: the AWS family gets the Tagging API
// client for the Tagging API write; a family whose post-create write has
// no writer is refused by name, never handed a nil client in silence; a
// family that never needs one is refused rather than handed the AWS client;
// a provider no family claims is refused by its address.
func TestMarkerWriterChosenByTheWrite(t *testing.T) {
	p := &statelessProviders{}
	aws := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	graph := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("graph")}

	w, err := p.markerWriterFor(substrate.AWS, true, aws, substrate.WriteTaggingAPI)
	if err != nil || w == nil {
		t.Errorf("AWS family: writer %v, err %v; want the Tagging API client", w, err)
	}

	w, err = p.markerWriterFor(bindingFamily{substrate.AWS}, true, graph, "graph-binding")
	if w != nil || err == nil {
		t.Fatalf("family with no writer: writer %v, err %v; want a refusal", w, err)
	}
	for _, name := range []string{"graph", `"graph-binding"`} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("family with no writer: refusal %q does not name %s", err, name)
		}
	}

	kube := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	if w, err = p.markerWriterFor(substrate.Kubernetes, true, kube, substrate.WriteTaggingAPI); w != nil || err == nil {
		t.Errorf("Kubernetes asked for a Tagging API write: writer %v, err %v; want a refusal", w, err)
	}

	unclaimed := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("nobody")}
	if w, err = p.markerWriterFor(nil, false, unclaimed, substrate.WriteTaggingAPI); w != nil || err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("unclaimed provider: writer %v, err %v; want a refusal naming the provider", w, err)
	}
}

// bindingFamily is a family that marks after create through a write no
// writer serves.
type bindingFamily struct{ substrate.Substrate }

func (bindingFamily) Name() string { return "graph" }
func (bindingFamily) Writes(markers.Surface) substrate.Writes {
	return substrate.Writes{Create: substrate.WriteInCreate, PostCreate: "graph-binding"}
}
func (bindingFamily) MarkerWriter(addrs.AbsProviderConfig) substrate.Write { return "graph-binding" }
