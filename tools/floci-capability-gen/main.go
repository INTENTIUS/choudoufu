// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// floci-capability-gen (re)generates live/floci-capabilities.json's entry
// for one floci image digest by probing a running instance of that image,
// closing the loop live/flocicap.go's package doc names: a bumped floci
// image should never have to wait for the next batch of e2e work to
// rediscover, by trial and error, which services and types it still does
// not implement.
//
// Two probe modes, both safe to run against a floci instance mid-use (both
// are read-only against AWS-shaped state; the health probe makes no calls
// at all, and the Cloud Control sweep only ever calls ListResources):
//
//   - -mode=services (the default's first half): GETs
//     <endpoint>/_localstack/health, which floci answers with the flat
//     "service name -> status" map its own /_localstack/health always has -
//     live/e2e/estates/stragglers/README.md's own "Floci coverage"
//     section reads this same endpoint by hand. Every service name the
//     response includes is recorded implemented, with its reported status
//     as evidence. A service this tool is watching for - every service
//     name already recorded for ANY digest in the committed manifest,
//     plus anything passed via -watch - that the response does NOT
//     include is recorded unimplemented. This is why the manifest is
//     cumulative rather than needing a fixed universal service roster: a
//     hand investigation that finds a new absent service and records it
//     once (tools/estate-gen/overrides.go's "hand judgment, mechanically
//     replayed thereafter" split) makes every future -mode=services run
//     re-verify it automatically, for every image bumped after.
//
//   - -mode=cloudcontrol (the default's second half): sweeps every
//     registry-ratified type (internal/live/identity.AdmittedTypes, joined
//     through live/mapping.json + live/registry.json exactly the way
//     internal/live/discovery's own enumeration-source selection does) and
//     calls Cloud Control's ListResources for each one that join says is
//     listable - the exact same call tools/cloudcontrol-probe makes by
//     hand for one type at a time, generalized into a full sweep with
//     classification. A clean result (even an empty list) means
//     implemented; an UnsupportedOperation/UnknownOperationException-shaped
//     API error means unimplemented; a response Cloud Control's own client
//     cannot parse as its ordinary error shape (the HTML-error-page
//     signature the databases and stragglers cohort READMEs both document
//     for a router-recognized-but-broken handler) means broken. Written
//     under the "cloudcontrol-list" mechanism - see
//     internal/live/flocitest.CloudControlListCapabilityGate - never
//     conflated with the ordinary create/read path's own mechanism="" gaps,
//     which this tool does not attempt to probe generically (no minimal
//     per-type Create recipe is safe to invent without a live image to
//     verify it against; those stay hand-curated, exactly like every
//     "types" row this manifest ships with today).
//
// Usage, against a floci instance already running (this tool starts none
// itself):
//
//	docker run -d --rm -p 4566:4566 --name floci-probe ghcr.io/lex00/floci@sha256:...
//	go run ./tools/floci-capability-gen -endpoint http://localhost:4566 -image ghcr.io/lex00/floci@sha256:...
//
// -image accepts a ref already pinned by digest (repo@sha256:...) directly,
// or a mutable tag/name, in which case the tool resolves the exact digest
// docker actually pulled with `docker inspect --format
// '{{index .RepoDigests 0}}' <ref>` before writing anything - the whole
// point of keying by digest is that a mutable tag never gets to stand in
// for one, so a ref that cannot be resolved this way is a hard error, not a
// guess.
//
//   - -mode=tagging (not part of the default "all" - it creates and deletes
//     real resources through each recipe's own service API, so unlike the
//     two probes above it is not safe to run against a floci instance
//     mid-use): drives internal/live/discovery's estate-wide TaggingSweep
//     path from the outside. tagging.go's taggingRecipes creates one
//     minimal, tagged resource per curated type via the aws CLI directly
//     (never through Cloud Control, never through terraform), confirms the
//     tags landed through that same service's own native read call, then
//     makes one unfiltered resourcegroupstaggingapi GetResources sweep -
//     the same shape internal/live/discovery's SourceTagging path uses -
//     and classifies each recipe's type by whether its ARN turns up there.
//     Written under the "tagging-sweep" mechanism, replacing every existing
//     row under that mechanism the same way -mode=cloudcontrol replaces
//     "cloudcontrol-list" rows - this mode is what generates that bucket
//     now, rather than it staying hand-curated indefinitely.
//
// Every run merges into the committed live/floci-capabilities.json rather
// than replacing it: -mode=services rewrites only the resolved digest's own
// services array, -mode=cloudcontrol rewrites only that digest's
// mechanism="cloudcontrol-list" type rows, -mode=tagging rewrites only that
// digest's mechanism="tagging-sweep" type rows, and every other row (every
// mechanism="" entry, and every other digest's own entries) is carried
// through untouched. No mode ever writes anything for a digest it could not
// resolve or an endpoint it could not reach - an honest "could not check"
// stays absent from the manifest, which
// [FlociServiceCapability]/[FlociTypeCapability] already read as "not yet
// investigated", never as a fabricated "implemented".
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/floci-capability-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// manifestRel is where the generated artifact is committed, relative to the
// repository root - the same path live/flocicap.go embeds.
const manifestRel = "live/floci-capabilities.json"

func main() {
	endpoint := flag.String("endpoint", "", "the running floci instance to probe, e.g. http://localhost:4566 (required)")
	image := flag.String("image", "", "the floci image ref this endpoint is running: repo@sha256:... directly, or a mutable tag/name to resolve via `docker inspect` (required)")
	region := flag.String("region", "us-east-1", "region for the Cloud Control sweep's SigV4 credential scope; floci does not verify signatures, so this rarely matters")
	mode := flag.String("mode", "all", `which probe(s) to run: "services", "cloudcontrol", "tagging", or "all"`)
	watch := flag.String("watch", "", "comma-separated extra service ids to check for in -mode=services, beyond every service id already recorded for any digest in the manifest")
	out := flag.String("out", "", "manifest path; empty defaults to live/floci-capabilities.json")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall timeout for the probe(s)")
	flag.Parse()

	if err := run(*endpoint, *image, *region, *mode, *watch, *out, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "floci-capability-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(endpoint, image, region, mode, watch, out string, timeout time.Duration) error {
	if endpoint == "" {
		return fmt.Errorf("-endpoint is required (a running floci instance; this tool starts none itself)")
	}
	if image == "" {
		return fmt.Errorf("-image is required")
	}
	switch mode {
	case "services", "cloudcontrol", "tagging", "all":
	default:
		return fmt.Errorf("-mode must be \"services\", \"cloudcontrol\", \"tagging\" or \"all\", got %q", mode)
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(root, manifestRel)
	}

	digest, err := resolveDigest(image)
	if err != nil {
		return fmt.Errorf("resolving %s to a digest: %w", image, err)
	}

	art, err := loadManifest(out)
	if err != nil {
		return fmt.Errorf("loading the existing manifest at %s: %w", out, err)
	}
	img := art.imageEntry(digest)
	img.Ref = image

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if mode == "services" || mode == "all" {
		watchlist := art.allKnownServices()
		for _, w := range strings.Split(watch, ",") {
			w = strings.TrimSpace(w)
			if w != "" {
				watchlist[w] = true
			}
		}
		rows, err := probeServices(ctx, endpoint, watchlist)
		if err != nil {
			return fmt.Errorf("probing %s/_localstack/health: %w", endpoint, err)
		}
		img.Services = rows
		fmt.Fprintf(os.Stderr, "floci-capability-gen: services: %d recorded (of %d watched)\n", len(rows), len(watchlist))
	}

	if mode == "cloudcontrol" || mode == "all" {
		rows, checked, err := probeCloudControl(ctx, root, endpoint, region)
		if err != nil {
			return fmt.Errorf("running the Cloud Control sweep: %w", err)
		}
		img.replaceMechanism("cloudcontrol-list", rows)
		fmt.Fprintf(os.Stderr, "floci-capability-gen: cloudcontrol-list: %d rows recorded (of %d listable types checked)\n", len(rows), checked)
	}

	if mode == "tagging" {
		rows, checked, err := probeTagging(ctx, endpoint, region)
		if err != nil {
			return fmt.Errorf("running the tagging-index sweep: %w", err)
		}
		img.replaceMechanism("tagging-sweep", rows)
		fmt.Fprintf(os.Stderr, "floci-capability-gen: tagging-sweep: %d rows recorded (of %d recipes that created cleanly)\n", len(rows), checked)
	}

	art.setImageEntry(digest, img)
	return writeManifest(out, art)
}
