// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// largeset-gen writes the multi-estate fixture for large change sets (issue
// #1750, epic #1749): N estate roots, each with its own estate.chdf.hcl, all
// calling one shared module at version A or B. The rendering lives in
// internal/live/largeset so the floci baseline test generates the same bytes
// in-process; see that package's doc comment for the shape.
//
//	go run ./tools/largeset-gen -estates 5 -module-version A -out /tmp/ls
//	go run ./tools/largeset-gen -estates 5 -module-version B -out /tmp/ls   # the bump, in place
//
// The OCI variant calls the module through a pinned oci:// source instead of
// a relative path, and -pin-b moves a subset of roots to version B:
//
//	go run ./tools/largeset-gen publish -registry localhost:4890 -ca ca.pem
//	go run ./tools/largeset-gen -estates 5 -module-version A -source oci -registry localhost:4890 -out /tmp/lso
//	go run ./tools/largeset-gen -estates 5 -module-version A -source oci -registry localhost:4890 -pin-b e02,e04 -out /tmp/lso
//
// Why a new tool rather than tools/estate-gen or tools/terralith-gen (the
// decision #1750 asks to be stated): estate-gen emits one block per admitted
// TYPE across a cohort, from provider schemas, into one root, and needs a
// provider init to run at all; terralith-gen emits one stock-shaped root and
// by design never writes an estate declaration or a marker. This fixture is
// the shape neither produces - many roots, each its own estate, one shared
// module, cross-estate reads - and bending either tool into it would add a
// third mode to a generator whose existing tests pin a different contract.
// It needs no schema and no network, so it is a pure function of its flags.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/intentius/choudoufu/internal/live/largeset"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "publish" {
		if err := publish(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "largeset-gen publish: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := generate(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "largeset-gen: %v\n", err)
		os.Exit(1)
	}
}

func generate(args []string) error {
	fs := flag.NewFlagSet("largeset-gen", flag.ContinueOnError)
	estates := fs.Int("estates", 5, "number of estate roots (N); 5 is one full block of the shape")
	version := fs.String("module-version", "A", "the shared module's version: A or B")
	out := fs.String("out", "", "output directory (required); regenerating into it rewrites only the files the fixture owns")
	prefix := fs.String("prefix", "ls", "prefix for every estate name and every name the fixture creates in the account")
	source := fs.String("source", "local", "how roots reach the module: local (relative path) or oci (pinned oci:// source)")
	registry := fs.String("registry", "", "OCI registry host:port, for -source oci")
	pinB := fs.String("pin-b", "", "comma-separated estates (e02,e04) pinned at version B, for -source oci")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	var pins []string
	if *pinB != "" {
		pins = strings.Split(*pinB, ",")
	}
	m, err := largeset.Write(*out, largeset.Options{
		Estates: *estates, ModuleVersion: largeset.Version(*version), Prefix: *prefix,
		Source: largeset.Source(*source), Registry: *registry, PinB: pins,
	})
	if err != nil {
		return err
	}
	fmt.Printf("largeset-gen: wrote %d estates (source=%s, module %s) to %s\n", len(m.Estates), m.Source, m.ModuleVersion, *out)
	return nil
}

func publish(args []string) error {
	fs := flag.NewFlagSet("largeset-gen publish", flag.ContinueOnError)
	registry := fs.String("registry", "", "OCI registry host:port (required)")
	ca := fs.String("ca", "", "PEM file the registry's TLS certificate chains to (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *registry == "" || *ca == "" {
		return fmt.Errorf("-registry and -ca are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, v := range []largeset.Version{largeset.VersionA, largeset.VersionB} {
		d, err := largeset.Publish(ctx, *registry, *ca, v)
		if err != nil {
			return fmt.Errorf("version %s: %w", v, err)
		}
		fmt.Printf("published %s/%s:%s (version %s) %s\n", *registry, largeset.OCIRepository, v.OCITag(), v, d)
	}
	return nil
}
