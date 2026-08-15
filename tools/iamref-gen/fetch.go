// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The two condition keys marker governance rests on. aws:ResourceTag is what
// an estate grant policy scopes on ("this principal may act on resources
// tagged tofu-estate=prod"); aws:TagKeys is what live/MARKERS.md's
// marker-protection SCP conditions its Deny on.
const (
	resourceTagKey = "aws:ResourceTag/${TagKey}"
	tagKeysKey     = "aws:TagKeys"
)

// indexEntry is one row of the reference's own index.
type indexEntry struct {
	Service  string `json:"service"`
	URL      string `json:"url"`
	Modified int64  `json:"modified"`
}

// serviceDoc is the slice of one service document this tool reads.
type serviceDoc struct {
	Name    string `json:"Name"`
	Actions []struct {
		Name                string   `json:"Name"`
		ActionConditionKeys []string `json:"ActionConditionKeys"`
		Resources           []struct {
			ConditionKeys []string `json:"ConditionKeys"`
		} `json:"Resources"`
	} `json:"Actions"`
}

// docAction is one action's condition-key surface, flattened.
type docAction struct {
	keys map[string]bool
}

// supports reports whether the action evaluates a condition key, at the
// action level or on any of its resource types. Both count: an IAM policy
// statement conditioned on the key constrains the call either way, and which
// level it comes from is a distinction a policy author does not make.
func (a docAction) supports(key string) bool { return a.keys[key] }

// actionsListingResourceTag counts how many of the service's actions NAME
// aws:ResourceTag/${TagKey} on at least one of their resource types, and how
// many actions there are.
//
// Naming, not supporting. lambda:GetFunction carries a resource entry with no
// ConditionKeys at all while Lambda does support tag-based authorization, so
// the zero this returns for Lambda is the reference declining to enumerate
// rather than IAM declining to evaluate.
func (d *serviceDoc) actionsListingResourceTag() (listing, total int) {
	for _, a := range d.Actions {
		total++
		for _, r := range a.Resources {
			for _, k := range r.ConditionKeys {
				if k == resourceTagKey {
					listing++
					goto next
				}
			}
		}
	next:
	}
	return listing, total
}

func (d *serviceDoc) action(name string) (docAction, bool) {
	for _, a := range d.Actions {
		if !strings.EqualFold(a.Name, name) {
			continue
		}
		keys := map[string]bool{}
		for _, k := range a.ActionConditionKeys {
			keys[k] = true
		}
		for _, r := range a.Resources {
			for _, k := range r.ConditionKeys {
				keys[k] = true
			}
		}
		return docAction{keys: keys}, true
	}
	return docAction{}, false
}

// cacheDir is where fetched documents live, the same convention every other
// fetching generator here follows.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "choudoufu", "iamref-gen")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a cache directory
		return "", err
	}
	return dir, nil
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// fetchIndex reads the reference's service index, cached.
//
// The index's own newest `modified` timestamp is recorded in the artifact as
// its pin: it moves when AWS republishes, which is what tells a reader
// whether a regeneration would change anything.
func fetchIndex(refresh bool) (map[string]indexEntry, int64, error) {
	data, err := cachedGet("index.json", indexURL, refresh)
	if err != nil {
		return nil, 0, err
	}
	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, 0, fmt.Errorf("decoding the reference index: %w", err)
	}
	if len(entries) == 0 {
		return nil, 0, fmt.Errorf("the reference index is empty")
	}
	out := make(map[string]indexEntry, len(entries))
	var newest int64
	for _, e := range entries {
		out[e.Service] = e
		if e.Modified > newest {
			newest = e.Modified
		}
	}
	return out, newest, nil
}

// fetchService reads one service document, cached under its own modified
// timestamp so a republished document is re-fetched and an unchanged one is
// not.
func fetchService(e indexEntry, refresh bool) (*serviceDoc, error) {
	name := fmt.Sprintf("%s__%d.json", e.Service, e.Modified)
	data, err := cachedGet(name, e.URL, refresh)
	if err != nil {
		return nil, err
	}
	var doc serviceDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", e.Service, err)
	}
	return &doc, nil
}

// cachedGet is the whole fetch policy: read the cache unless -refresh, and
// write what the network returns back into it.
func cachedGet(name, url string, refresh bool) ([]byte, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name)
	if !refresh {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // a cache path this tool owns
			return data, nil
		}
	}

	resp, err := httpClient.Get(url) //nolint:gosec,noctx // a fixed upstream URL
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a cache file
		return nil, fmt.Errorf("caching %s: %w", name, err)
	}
	return data, nil
}
