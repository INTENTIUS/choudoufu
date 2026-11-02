// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"
)

// LiveSetup is where a root's records go and which estate they belong to
// (GitHub issue #2293). It is printed as one line on every init and plan
// under a live block and carried in live-check -json; the JSON names are the
// contract posted on the issue.
type LiveSetup struct {
	Estate       string         `json:"estate,omitempty"`
	EstateSource string         `json:"estate_source"`
	RecordStore  LiveSetupStore `json:"record_store"`
}

// LiveSetupStore is the record store half of [LiveSetup].
type LiveSetupStore struct {
	// Kind is "local", "s3", "gcs", "azurerm" or "kubernetes".
	Kind string `json:"kind"`
	// Bucket is the s3 or gcs bucket, "<account>/<container>" for azurerm,
	// the namespace for kubernetes, and the directory for local.
	Bucket string `json:"bucket"`
	// Prefix is where this estate's records go inside it.
	Prefix string `json:"prefix"`
	// Source is "record_store", "backend" or "implied".
	Source string `json:"source"`
	// DeclaredAt is "file:line" of the block that decided the store.
	DeclaredAt string `json:"declared_at,omitempty"`
	// Note says why a backend block was there and not used.
	Note string `json:"note,omitempty"`
}

// LiveSetupView prints a [LiveSetup] line on stderr.
type LiveSetupView struct {
	view *View
}

// NewLiveSetup returns the printer for the record store line.
func NewLiveSetup(view *View) *LiveSetupView {
	return &LiveSetupView{view: view}
}

// Print writes the line on stderr, so a -json stdout stays one document.
func (v *LiveSetupView) Print(s LiveSetup) {
	_, _ = v.view.streams.Eprintln(LiveSetupLine(s))
}

// LiveSetupLine is the printed form of s:
//
//	Records: s3 bucket "acme-tfstate" under tofu-records/prod-network/ (derived from backend "s3" at backend.tf:2); estate "prod-network" (from the backend key)
func LiveSetupLine(s LiveSetup) string {
	var b strings.Builder
	st := s.RecordStore
	b.WriteString("Records: ")
	switch st.Kind {
	case "local":
		fmt.Fprintf(&b, "local directory %s", st.Bucket)
	case "azurerm":
		fmt.Fprintf(&b, "azurerm container %q", st.Bucket)
	case "kubernetes":
		fmt.Fprintf(&b, "kubernetes namespace %q", st.Bucket)
	default:
		fmt.Fprintf(&b, "%s bucket %q", st.Kind, st.Bucket)
	}
	if st.Prefix != "" {
		fmt.Fprintf(&b, " under %s", st.Prefix)
	}
	switch st.Source {
	case "backend":
		fmt.Fprintf(&b, " (derived from backend %q at %s)", st.Kind, st.DeclaredAt)
	case "record_store":
		fmt.Fprintf(&b, " (record_store at %s)", st.DeclaredAt)
	default:
		if st.Note != "" {
			fmt.Fprintf(&b, " (implied: %s)", st.Note)
		} else {
			b.WriteString(" (implied: no record_store block and no backend block)")
		}
	}
	b.WriteString("; ")
	if s.Estate == "" {
		b.WriteString("no estate name resolved")
		return b.String()
	}
	fmt.Fprintf(&b, "estate %q", s.Estate)
	switch s.EstateSource {
	case "explicit":
		b.WriteString(" (declared)")
	case "tags":
		b.WriteString(" (from the tofu-estate tags)")
	case "backend_key":
		b.WriteString(" (from the backend key)")
	case "root_path":
		b.WriteString(" (from the root's path in the repository)")
	}
	return b.String()
}
