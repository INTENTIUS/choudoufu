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
// Every mode except -mode=services writes to the instance it probes, so run
// this tool against a throwaway floci container, never one another agent is
// mid-crossing on. That is a change from an earlier version of this tool,
// which called only ListResources and was therefore safe mid-use - and the
// reason for the change is the whole point of the -mode=cloudcontrol
// rewrite: a call that returns is not a service that answers, and there is
// no way to tell those apart without putting an object in front of the list
// and seeing whether it comes back.
//
// The probe modes:
//
//   - -mode=services (the default's first half): GETs
//     <endpoint>/_localstack/health, which floci answers with the flat
//     "service name -> status" map its own /_localstack/health always has -
//     live/e2e/estates/stragglers/README.md's own "Floci coverage"
//     section reads this same endpoint by hand. Every service name the
//     response includes AND that this tool is watching for is recorded
//     implemented, with its reported status as evidence; a service the
//     response names that nothing is watching for is not recorded at all,
//     so this grain's coverage is exactly the watchlist (#276 - the health
//     response names 82 services against the manifest's 4). A service this
//     tool is watching for - every service name already recorded for ANY
//     digest in the committed manifest, plus anything passed via -watch -
//     that the response does NOT include is recorded unimplemented. This is
//     why the manifest is cumulative rather than needing a fixed universal
//     service roster: a
//     hand investigation that finds a new absent service and records it
//     once (tools/estate-gen/overrides.go's "hand judgment, mechanically
//     replayed thereafter" split) makes every future -mode=services run
//     re-verify it automatically, for every image bumped after.
//
//   - -mode=cloudcontrol (the default's second half): sweeps every
//     registry-ratified type (internal/live/identity.AdmittedTypes, joined
//     through live/mapping.json + live/registry.json exactly the way
//     internal/live/discovery's own enumeration-source selection does) and,
//     for each one that join says is listable, ROUND TRIPS it: list, then
//     create one resource of the type through Cloud Control with an empty
//     desired state, then list again and look for the identifier the create
//     named.
//
//     The round trip is the point. An emulator whose ListResources handler
//     is a stub answers an empty ResourceDescriptions with no error at all,
//     which is indistinguishable from a real list of an empty account - so
//     "the call returned" cannot be evidence that the service answers, and
//     an earlier version of this sweep that recorded exactly that put 645
//     "implemented" rows into the manifest for one image, most of which a
//     discovery leg cannot rely on. AWS::CloudFront::CachePolicy is the
//     worked example: create succeeds, the object is real (CloudFront's own
//     list-cache-policies returns it), and Cloud Control's ListResources
//     stays empty.
//
//     Verdicts, and what each is evidence of: implemented means the created
//     resource came back in the list, the only path to that word here;
//     unimplemented means either the router refuses ListResources
//     (UnsupportedOperation/UnknownOperationException) or the created
//     resource was absent from the list it did return; broken means a
//     response Cloud Control's own client cannot parse as its ordinary
//     error shape (the HTML-error-page signature the databases and
//     stragglers cohort READMEs both document for a
//     router-recognized-but-broken handler); unverified means the handler
//     answered but nothing was established - overwhelmingly because no
//     resource of the type could be created to look for. Every evidence
//     string says which calls were made and what they returned, so a reader
//     can tell a round trip from a bare call without consulting this file.
//
//     Nothing about which types get round-tripped is written down anywhere:
//     the type list is internal/live/identity.AdmittedTypes joined through
//     the registry, and the desired state is "{}" for every one of them,
//     because no source in this checkout carries per-type required
//     properties and inventing them would be the hand-written type list
//     this generator exists to avoid. A type whose create is refused
//     therefore lands in unverified rather than being guessed at.
//
//     Written under the "cloudcontrol-list" mechanism - see
//     internal/live/flocitest.CloudControlListCapabilityGate - never
//     conflated with the ordinary create/read path's own mechanism="" gaps,
//     which this tool does not attempt to probe generically; those stay
//     hand-curated, exactly like every "types" row this manifest ships with
//     under mechanism="".
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
// # Reproducibility
//
// Re-probing the same image must produce the same file, or the artifact's
// diff stops carrying information. Measured 2026-08-17 against
// sha256:a1c729f4: three -mode=cloudcontrol runs, two against separate
// fresh containers and one against a container already holding the previous
// run's ~600 created resources, agreed on the verdict for all 610 rows -
// zero status differences, including the dirty one.
//
// Getting there took removing two things from the evidence text that varied
// per run: the identifier the emulator generates for each created resource
// (random every time - it alone made 590 of 610 rows differ between two
// runs) and, on the found path, how many other resources the list happened
// to be carrying (which grows as a re-run's own creates accumulate). The
// test named for that property in sweep_test.go holds the line. The
// not-found path DOES keep its count, because zero-versus-many is the
// difference between a list handler that is a stub and one answering about
// something else; that number is stable on a fresh container, which is the
// only kind this tool should be pointed at.
//
// The sweep itself is cheap: 610 listable types, three or four calls each,
// 12 seconds against a warm local container.
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
	timeout := flag.Duration("timeout", 30*time.Minute, "overall timeout for the probe(s); the cloudcontrol round trip measured 12s over 610 types against a warm local container, so this is headroom for a cold one, not an estimate of the cost")
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
