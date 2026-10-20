// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command armtypes-gen writes internal/live/armtypes/table_generated.txt:
// for every ARM resource type and API version in the type data azure/azapi
// embeds, whether the request body takes `tags`, `name` and `location`
// (the Azure epic, #1120; the taggability ruling, #1122).
//
// The rule and its source in azapi are in internal/live/armtypes's package
// doc. This command is a thin wrapper: it reads the checkout's tag and
// commit, calls armtypes.Generate, and writes the rendered table.
//
// Usage:
//
//	go run ./tools/armtypes-gen -azapi-src <azapi checkout at the pinned tag>
//	go run ./tools/armtypes-gen -azapi-src <dir> -check   # fail on drift, write nothing
//
// The checkout must sit exactly on a release tag, because the table records
// which azapi release it describes. The pin for the Azure lane is v2.13.0.
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
		src   = flag.String("azapi-src", "", "an azure/terraform-provider-azapi checkout at a release tag (required)")
		root  = flag.String("root", ".", "repository root")
		check = flag.Bool("check", false, "compare the committed table against the source and exit non-zero on any difference")
	)
	flag.Parse()
	if *src == "" {
		fail(fmt.Errorf("-azapi-src is required"))
	}

	version, commit, err := armtypes.SourcePin(*src)
	if err != nil {
		fail(err)
	}
	tbl, err := armtypes.Generate(*src, version, commit)
	if err != nil {
		fail(err)
	}
	want := tbl.Render()
	c := tbl.Counts()
	summary := fmt.Sprintf("azapi %s (%s): %d types, %d type-versions, %d taggable at latest, %d untaggable at latest, %d version-dependent, %d tags only via a discriminated body",
		version, commit, c.Types, c.TypeVersions, c.TaggableAtLatest, c.Types-c.TaggableAtLatest, c.VersionDependent, tbl.DiscriminatedTags)

	out := filepath.Join(*root, "internal", "live", "armtypes", armtypes.TablePath)
	if *check {
		got, err := os.ReadFile(out)
		if err != nil {
			fail(err)
		}
		if !bytes.Equal(got, want) {
			fmt.Fprintf(os.Stderr, "%s is stale against %s\nrun: go run ./tools/armtypes-gen -azapi-src %s\n", out, summary, *src)
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
	fmt.Fprintln(os.Stderr, "armtypes-gen:", err)
	os.Exit(1)
}
