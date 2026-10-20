// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command azurerm-tags-gen writes
// internal/live/armtypes/azurerm_tags_generated.json: for every azurerm
// managed resource type whose schema has `tags`, whether its Create sends
// the tags in the create call or writes them in a later call, and what
// azurerm's own validation of `tags` allows (the Azure epic, #1120; the
// #1122 ruling's "PostCreate: the azurerm types that tag in a separate
// call after create, generated from provider source").
//
// The analysis is armtypes.GenerateAzurermTags, with go/parser over the
// checkout and its vendor/ tree; its rule is in internal/live/armtypes's
// rmanalyze.go. This command reads the checkout's version and commit, runs
// it, and writes the rendered facts.
//
// Usage:
//
//	go run ./tools/azurerm-tags-gen -src <azurerm checkout at the pin> -schema <tofu providers schema -json output>
//	go run ./tools/azurerm-tags-gen -src <dir> -schema <file> -check   # fail on drift, write nothing
//
// The pin for the Azure lane is azurerm 5.10.0.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/intentius/choudoufu/internal/live/armtypes"
)

func main() {
	var (
		src    = flag.String("src", "", "a hashicorp/terraform-provider-azurerm checkout at the pinned tag (required)")
		schema = flag.String("schema", "", "`tofu providers schema -json` output that includes hashicorp/azurerm at the same version (required)")
		root   = flag.String("root", ".", "repository root")
		check  = flag.Bool("check", false, "compare the committed facts against the source and exit non-zero on any difference")
	)
	flag.Parse()
	if *src == "" || *schema == "" {
		fail(fmt.Errorf("-src and -schema are required"))
	}
	version, commit, err := armtypes.AzurermSourcePin(*src)
	if err != nil {
		fail(err)
	}
	facts, err := armtypes.GenerateAzurermTags(*src, *schema, version, commit)
	if err != nil {
		fail(err)
	}
	c := facts.Counts()
	summary := fmt.Sprintf("azurerm %s (%s): %d types with tags, %d in-create, %d after-create, %d unknown, %d with tighter tag validation",
		version, commit, c.Tagged, c.InCreate, c.AfterCreate, c.Unknown, c.Limited)
	want := facts.Render()

	out := filepath.Join(*root, "internal", "live", "armtypes", armtypes.RMTagsPath)
	if *check {
		got, err := os.ReadFile(out)
		if err != nil {
			fail(err)
		}
		if !bytes.Equal(got, want) {
			fmt.Fprintf(os.Stderr, "%s is stale against %s\nrun: go run ./tools/azurerm-tags-gen -src %s -schema %s\n", out, summary, *src, *schema)
			os.Exit(1)
		}
		fmt.Printf("%s matches %s\n", out, summary)
		return
	}
	if err := os.WriteFile(out, want, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s (%d bytes): %s\n", out, len(want), summary)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "azurerm-tags-gen:", err)
	os.Exit(1)
}
