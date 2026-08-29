// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// tagverbs-gen generates live/tag-verbs.json, the machine-derived join that
// replaces internal/live/foreign/classify.go's hand-written ec2Types table
// (issue #52, the botocore side): which AWS API operation tags a resource of
// a given service, extracted from botocore's own service models rather than
// stated by hand.
//
// botocore (github.com/boto/botocore) ships one JSON service model per AWS
// service, data/<service>/<version>/service-2.json, each declaring its own
// operations and their input shapes. A service's tagging operation is found
// by pattern: an operation name that looks tag-ish (TagResource, CreateTags,
// AddTags, TagQueue, AddTagsToCertificate, ChangeTagsForResource, ...) and
// is not a read or removal verb (ListTagsForResource, UntagResource, ...),
// whose input shape carries both a *Tags-suffixed member and at least one
// other member - the resource identifier the tags are written onto. A
// service with zero such operations (S3, which tags through a bucket/object
// "Tagging" wrapper, not a *Tags member) or more than one (IAM, one TagX per
// entity type; ACM, both AddTagsToCertificate and TagResource; CloudWatch
// Logs, both TagLogGroup and TagResource) resolves to "unknown" rather than
// a guess - see extract.go's classifyTagOps.
//
// Only the services live/mapping.json's mapped set (cfn_type non-null rows)
// actually names are fetched, not all ~460 of botocore's - the same
// "generate CANDIDATES from a pinned source, not the whole upstream"
// discipline issue #52's design section states. A CFN service segment
// (AWS::Lambda::Function -> "Lambda") is joined to botocore's directory name
// by normalizing both to a bare lowercase alphanumeric run and matching
// exactly - the same normalizeServiceID trick
// tools/mapping-gen/namesdata.go's service-alias join uses for the same
// reason (spelling conventions differ: hyphens in "application-autoscaling"
// vs none in "ApplicationAutoScaling"). That alone resolves the large
// majority of the mapped set; a short, explicitly-justified alias table in
// resolve.go covers the handful of genuine abbreviations (CertificateManager
// -> acm, ElasticLoadBalancingV2 -> elbv2) that no normalization finds. A
// segment neither path resolves is recorded as "no botocore directory found"
// - honest, not guessed.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/tagverbs-gen
//
// It needs network for the first fetch of the repository tree and of each
// resolved service's model (or a warm cache under the OS user cache
// directory, namespaced by the pinned botocore tag the same way
// tools/importdocs-gen namespaces its doc cache by provider version).
//
// -accept stamps the artifact header's accepted field with today's date,
// the same vocabulary tools/importdocs-gen's and tools/survey-gen's -accept
// flags document: omitting it leaves the field unset, so an unreviewed
// regeneration shows up in the diff as the accepted date disappearing.
//
// -render (issue #421, the same vocabulary tools/survey-gen's own -render
// flag uses) rewrites site/content/docs/use/reference.md's spans from the
// already-committed live/tag-verbs.json instead of regenerating it, so a
// doc-only drift needs no network to repair:
//
//	go run ./tools/tagverbs-gen -render
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// mappingJSONRel is where live/mapping.json's mapped set is read from.
// tagVerbsJSONRel is where the generated artifact is committed.
const (
	mappingJSONRel  = "live/mapping.json"
	tagVerbsJSONRel = "live/tag-verbs.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/tagverbs-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	accept := flag.Bool("accept", false, "stamp the artifact header's accepted field with today's date")
	cacheDirOverride := flag.String("cache-dir", "", "use this directory as the fetch cache instead of the OS user cache directory")
	render := flag.Bool("render", false, "rewrite site/content/docs/use/reference.md's spans from the committed live/tag-verbs.json instead of regenerating the artifact (needs no network)")
	flag.Parse()

	if *render {
		if err := runRender(); err != nil {
			fmt.Fprintf(os.Stderr, "tagverbs-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*accept, *cacheDirOverride, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "tagverbs-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(accept bool, cacheDirOverride string, log *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	services, err := loadMappedServices(filepath.Join(root, mappingJSONRel))
	if err != nil {
		return fmt.Errorf("reading the mapped service set from %s: %w", mappingJSONRel, err)
	}
	fmt.Fprintf(log, "tagverbs-gen: %d distinct CFN services in the mapped set\n", len(services))

	cacheDir := cacheDirOverride
	if cacheDir == "" {
		cacheDir, err = defaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving the fetch cache directory: %w", err)
		}
	}

	ctx := context.Background()
	tree, err := fetchTree(ctx, cacheDir, httpFetch)
	if err != nil {
		return fmt.Errorf("fetching the botocore repository tree: %w", err)
	}
	dirVersions := latestVersions(tree)
	fmt.Fprintf(log, "tagverbs-gen: %d botocore service directories at %s\n", len(dirVersions), botocoreTag)

	rows := make([]Row, 0, len(services))
	for _, svc := range services {
		rows = append(rows, buildRow(ctx, cacheDir, svc, dirVersions, httpFetch, log))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })

	art := buildArtifact(rows)
	if accept {
		art.Accepted = time.Now().UTC().Format("2006-01-02")
	}

	data, err := art.marshal()
	if err != nil {
		return err
	}
	out := filepath.Join(root, tagVerbsJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	if err := renderTagVerbSpan(root, rows); err != nil {
		return err
	}

	fmt.Fprintf(log,
		"tagverbs-gen: wrote %s (%d services: %d single verb [%d composable], %d ambiguous, %d none, %d no directory)%s\n",
		tagVerbsJSONRel, art.Counts.Services, art.Counts.Single, art.Counts.Composable,
		art.Counts.Ambiguous, art.Counts.None, art.Counts.NoDirectory, acceptedSuffix(art.Accepted))
	return nil
}

func acceptedSuffix(accepted string) string {
	if accepted == "" {
		return ""
	}
	return ", accepted " + accepted
}

// buildRow resolves one CFN service segment to a botocore directory, fetches
// its model when resolved, and classifies its tagging operation. Any error
// fetching or parsing a resolved service's model is folded into the row as
// an honest "no directory"/"none" outcome with the error in Reason rather
// than aborting the whole sweep - a network hiccup on one of ~190 services
// should not lose the other ~189.
func buildRow(ctx context.Context, cacheDir, cfnService string, dirVersions map[string]string, fetch fetcher, log *os.File) Row {
	dir, ok := resolveDirectory(cfnService, dirVersions)
	if !ok {
		return Row{
			Service: cfnService,
			Reason:  "no botocore data directory normalizes (or is aliased) to this CFN service segment",
		}
	}
	version := dirVersions[dir]

	data, err := fetchServiceModel(ctx, cacheDir, dir, version, fetch)
	if err != nil {
		fmt.Fprintf(log, "tagverbs-gen: %s (%s): %v\n", cfnService, dir, err)
		return Row{
			Service:           cfnService,
			BotocoreDirectory: dir,
			BotocoreVersion:   version,
			DirectoryFound:    true,
			Reason:            fmt.Sprintf("fetching the service model failed: %v", err),
		}
	}

	model, err := parseServiceModel(data)
	if err != nil {
		return Row{
			Service:           cfnService,
			BotocoreDirectory: dir,
			BotocoreVersion:   version,
			DirectoryFound:    true,
			Reason:            fmt.Sprintf("parsing the service model failed: %v", err),
		}
	}

	return classifyRow(cfnService, dir, version, model)
}
