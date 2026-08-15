// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// iamref-gen reads AWS's Service Authorization Reference - published as JSON
// at servicereference.us-east-1.amazonaws.com - and writes
// live/iam-reference.json: per service, whether the tagging action this fork
// performs can be scoped by aws:ResourceTag and aws:TagKeys.
//
// It exists because live/MARKERS.md publishes a marker-protection SCP and
// then admits it cannot vouch for it:
//
//	That action list is illustrative, not exhaustive or verified for every
//	admitted type. It has to be, since this fork tracks each type's tagging
//	verb but not its untagging one, so there is no generated artifact to
//	check it against.
//
// Issue #152. The same reference answers the other half - whether an estate
// grant policy conditioned on aws:ResourceTag/tofu-estate actually
// constrains a given service (#142).
//
// Nothing here is curated. The service list is derived from
// live/tag-verbs.json's own roster, and each service's IAM name is resolved
// from that artifact's iam_prefix_candidates against the reference's index
// rather than mapped by hand - see tools/tagverbs-gen for why the candidates
// are a list.
//
//	go run ./tools/iamref-gen              # cached where possible
//	go run ./tools/iamref-gen -refresh     # re-fetch everything
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	artifactRel   = "live/iam-reference.json"
	tagVerbsRel   = "live/tag-verbs.json"
	mappingRel    = "live/mapping.json"
	admissionRel  = "internal/live/identity/table_generated.go"
	indexURL      = "https://servicereference.us-east-1.amazonaws.com/"
	fetchParallel = 8
)

func main() {
	refresh := flag.Bool("refresh", false, "re-fetch every service document instead of using the cache")
	flag.Parse()

	if err := run(*refresh, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "iamref-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(refresh bool, out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	services, err := servicesInScope(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "iamref-gen: %d services the admission table reaches\n", len(services))

	index, indexModified, err := fetchIndex(refresh)
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "iamref-gen: %d services in the reference index\n", len(index))

	rows := make([]Row, len(services))
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchParallel)
	var mu sync.Mutex
	var fetchErr error

	for i, svc := range services {
		wg.Add(1)
		go func(i int, svc scopedService) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			row, err := buildRow(svc, index, refresh)
			if err != nil {
				mu.Lock()
				if fetchErr == nil {
					fetchErr = err
				}
				mu.Unlock()
				return
			}
			rows[i] = row
		}(i, svc)
	}
	wg.Wait()
	if fetchErr != nil {
		return fetchErr
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })

	art := Artifact{
		Source:        indexURL,
		GeneratedBy:   "tools/iamref-gen (go run ./tools/iamref-gen)",
		IndexModified: indexModified,
		Rows:          rows,
		Counts:        tally(rows),
	}

	data, err := marshal(art)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, artifactRel), data, 0o644); err != nil { //nolint:gosec // a committed artifact
		return fmt.Errorf("writing %s: %w", artifactRel, err)
	}

	if err := renderResourceTagSpan(root, rows); err != nil {
		return err
	}
	if err := renderSCPActionsSpan(root, rows); err != nil {
		return err
	}

	c := art.Counts
	fmt.Fprintf(errOut, "iamref-gen: wrote %s (%d services: %d resolved [%d unresolved, %d ambiguous]; "+
		"of those, %d tagging verbs checked [%d absent from the reference, %d never recorded]; "+
		"%d/%d name aws:ResourceTag, %d/%d name aws:TagKeys; "+
		"%d services name aws:ResourceTag on any action - a LOWER BOUND, see the artifact's own doc)\n",
		artifactRel, c.Services, c.Resolved, c.Unresolved, c.Ambiguous,
		c.TagActionFound, c.TagActionAbsent, c.NoTagVerbRecorded,
		c.ListsResourceTag, c.TagActionFound, c.ListsTagKeys, c.TagActionFound,
		c.ServicesListingResourceTag)
	return nil
}

// buildRow resolves one service and reads its condition-key support.
func buildRow(svc scopedService, index map[string]indexEntry, refresh bool) (Row, error) {
	row := Row{
		Service:    svc.CFNService,
		Candidates: svc.Candidates,
		TagAction:  svc.TagAction,
	}

	var resolved []string
	for _, c := range svc.Candidates {
		if _, ok := index[c]; ok {
			resolved = append(resolved, c)
		}
	}
	switch len(resolved) {
	case 0:
		row.Reason = "no candidate IAM prefix appears in the reference index"
		return row, nil
	case 1:
	default:
		row.Reason = fmt.Sprintf("ambiguous: %d candidates appear in the reference index", len(resolved))
		return row, nil
	}
	row.IAMPrefix = resolved[0]

	doc, err := fetchService(index[row.IAMPrefix], refresh)
	if err != nil {
		return Row{}, err
	}
	row.ActionsListingResourceTag, row.ActionsTotal = doc.actionsListingResourceTag()

	if svc.TagAction == "" {
		row.Reason = "live/tag-verbs.json records no tagging verb for this service"
		return row, nil
	}

	// The removal verb is resolved independently of the tagging one: a
	// service whose tagging call is ambiguous can still have exactly one
	// unambiguous untag action, and issue #152's SCP needs that one.
	row.UntagAction = svc.UntagAction
	if svc.UntagAction != "" {
		if ua, found := doc.action(svc.UntagAction); found {
			row.UntagActionFound = true
			row.UntagListsTagKeys = ua.supports(tagKeysKey)
		}
	}

	action, ok := doc.action(svc.TagAction)
	if !ok {
		row.Reason = fmt.Sprintf("the reference lists no action named %q for %s", svc.TagAction, row.IAMPrefix)
		return row, nil
	}
	row.TagActionFound = true
	row.ListsResourceTag = action.supports(resourceTagKey)
	row.ListsTagKeys = action.supports(tagKeysKey)
	return row, nil
}

func tally(rows []Row) Counts {
	var c Counts
	c.Services = len(rows)
	for _, r := range rows {
		if r.IAMPrefix == "" {
			if strings.HasPrefix(r.Reason, "ambiguous") {
				c.Ambiguous++
			} else {
				c.Unresolved++
			}
			continue
		}
		c.Resolved++
		if r.ActionsListingResourceTag > 0 {
			c.ServicesListingResourceTag++
		}

		switch {
		case r.TagActionFound:
			c.TagActionFound++
			if r.ListsResourceTag {
				c.ListsResourceTag++
			}
			if r.ListsTagKeys {
				c.ListsTagKeys++
			}
		case r.TagAction == "":
			c.NoTagVerbRecorded++
		default:
			c.TagActionAbsent++
		}
	}
	return c
}

// marshal renders the artifact deterministically, the same shape every other
// artifact in live/ uses.
func marshal(a Artifact) ([]byte, error) {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// repoRoot resolves the checkout root from this file's own location, the
// same trick every other generator here uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}
