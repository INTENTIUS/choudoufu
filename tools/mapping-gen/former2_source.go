// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Content pin and acquisition for former2's js/services/*.js (issue #52),
// the #42 pattern again: {digest, accepted, roster-ish summary}. former2 is
// fetched at a pinned commit sha (the tag concept doesn't apply to a repo
// that does not cut releases); the source is the whole js/services
// directory's concatenated text, fetched as a tarball of the pinned commit
// rather than ~150 individual file requests.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// former2Commit is the pinned iann0036/former2 commit sha (HEAD of master
// at the time issue #52 was implemented).
const former2Commit = "7d354df27db5a8260950021b2273758ba5df9f62"

// former2TarURL is the pinned commit's source tarball.
const former2TarURL = "https://github.com/iann0036/former2/archive/" + former2Commit + ".tar.gz"

// former2AcceptEnv accepts whatever the pinned commit currently serves when
// the cached/downloaded content no longer matches the committed digest.
const former2AcceptEnv = "CHOUDOUFU_ACCEPT_FORMER2"

// former2ServicesPrefix is the path, inside the tarball, of the directory
// this tool reads - every other file in the archive (the app itself, the
// other AWS SDK vendor bundle, etc.) is skipped during extraction.
const former2ServicesPrefix = "js/services/"

func former2CachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "choudoufu", "mapping-gen", "former2-"+former2Commit+"-services.txt"), nil
}

// f2Downloader fetches former2's pinned tarball - downloadFormer2Tar in
// production, swapped for a fake in tests.
type f2Downloader func(ctx context.Context, url string) ([]byte, error)

// acquireFormer2Services resolves former2's concatenated js/services/*.js
// text: an explicit override (a path to either a pre-extracted concatenated
// text file or a tarball - detected by trying tar-gz extraction first),
// the cached copy, or a fresh download. The cache always holds the
// concatenated text, not the tarball, since that's the only shape any
// caller ever needs again.
func acquireFormer2Services(ctx context.Context, cachePath, override string, log io.Writer) (string, error) {
	return acquireFormer2ServicesWith(ctx, cachePath, override, log, downloadFormer2Tar)
}

func acquireFormer2ServicesWith(ctx context.Context, cachePath, override string, log io.Writer, download f2Downloader) (string, error) {
	if override != "" {
		data, err := os.ReadFile(override) //nolint:gosec // an operator-supplied path
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", override, err)
		}
		text, err := extractFormer2ServicesText(data)
		if err != nil {
			// Not a tarball - treat the override itself as already the
			// concatenated services text (e.g. a hand-built testdata
			// fixture).
			text = string(data)
		}
		if err := cacheFormer2(cachePath, text); err != nil {
			return "", err
		}
		fmt.Fprintf(log, "mapping-gen: seeded the former2 cache at %s from %s\n", cachePath, override)
		return text, nil
	}

	data, err := os.ReadFile(cachePath) //nolint:gosec // a fixed cache path this tool itself wrote
	switch {
	case err == nil:
		fmt.Fprintf(log, "mapping-gen: using the cached former2 services text at %s\n", cachePath)
		return string(data), nil
	case !os.IsNotExist(err):
		return "", fmt.Errorf("reading the cached former2 services text at %s: %w", cachePath, err)
	}

	fmt.Fprintf(log, "mapping-gen: downloading %s\n", former2TarURL)
	tarData, err := download(ctx, former2TarURL)
	if err != nil {
		return "", err
	}
	text, err := extractFormer2ServicesText(tarData)
	if err != nil {
		return "", err
	}
	if err := cacheFormer2(cachePath, text); err != nil {
		return "", err
	}
	return text, nil
}

func cacheFormer2(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a cache directory
		return err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // a cached download
		return fmt.Errorf("caching former2 services text at %s: %w", path, err)
	}
	return nil
}

// extractFormer2ServicesText reads a gzipped tarball (the shape GitHub's
// archive/<sha>.tar.gz endpoint serves) and returns every js/services/*.js
// entry's content concatenated, sorted by path for determinism.
func extractFormer2ServicesText(tarGz []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarGz))
	if err != nil {
		return "", fmt.Errorf("opening the former2 tarball: %w", err)
	}
	defer gz.Close() //nolint:errcheck // a read-only gzip stream

	type entry struct {
		name string
		data []byte
	}
	var entries []entry

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading the former2 tarball: %w", err)
		}
		// Entries are rooted at "<repo>-<sha>/..."; only js/services/*.js
		// matters here.
		idx := strings.Index(hdr.Name, "/"+former2ServicesPrefix)
		if idx < 0 || !strings.HasSuffix(hdr.Name, ".js") {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("reading %s from the former2 tarball: %w", hdr.Name, err)
		}
		entries = append(entries, entry{name: hdr.Name[idx:], data: data})
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("the former2 tarball carried no %s*.js entries", former2ServicesPrefix)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(e.data)
		buf.WriteByte('\n')
	}
	return buf.String(), nil
}

func downloadFormer2Tar(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only response body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the response body from %s: %w", url, err)
	}
	return data, nil
}

func former2Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Former2Pin is the committed pin over former2's js/services/*.js
// concatenated text at former2Commit.
type Former2Pin struct {
	Digest     string `json:"digest"`
	Commit     string `json:"commit"`
	Accepted   string `json:"accepted"`
	RawRows    int    `json:"raw_rows"`
	UsableRows int    `json:"usable_rows"`
	Dropped    int    `json:"dropped"`
}

// Former2Artifact is the committed derived artifact
// (tools/mapping-gen/former2-rows.json): the pin, and every raw extracted
// row (filtering against the live rosters happens at buildMapping time,
// not here, so a TF or CFN roster bump can change which rows are usable
// without needing former2 re-fetched).
type Former2Artifact struct {
	Pin  Former2Pin   `json:"pin"`
	Rows []Former2Row `json:"rows"`
}

func buildFormer2Artifact(text string, cfnWithPrimaryID, cfnKnown, tfKnown map[string]bool, accepted string) Former2Artifact {
	rows := extractFormer2Rows(text)
	usable, drops := filterFormer2Rows(rows, cfnWithPrimaryID, cfnKnown, tfKnown)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TFType != rows[j].TFType {
			return rows[i].TFType < rows[j].TFType
		}
		return rows[i].CFNType < rows[j].CFNType
	})
	return Former2Artifact{
		Pin: Former2Pin{
			Digest:     former2Digest(text),
			Commit:     former2Commit,
			Accepted:   accepted,
			RawRows:    len(rows),
			UsableRows: len(usable),
			Dropped:    len(drops),
		},
		Rows: rows,
	}
}

func (a Former2Artifact) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// AssertFormer2Pinned enforces the pin the same way AssertNamesDataPinned
// does: refuses on any digest drift unless accept is set, in which case it
// warns and proceeds.
func AssertFormer2Pinned(text string, pin Former2Pin, accept bool, warn func(string)) error {
	digest := former2Digest(text)
	if digest == pin.Digest {
		return nil
	}
	msg := fmt.Sprintf(
		"former2's js/services/*.js at commit %s has moved since the pinned digest.\n\n  pinned    %s (accepted %s)\n  fetched   %s\n\nTo refresh the pin, rerun with -refresh-former2 and %s=1, review the regenerated tools/mapping-gen/former2-rows.json diff, and commit the new digest and commit sha.",
		former2Commit, pin.Digest, pin.Accepted, digest, former2AcceptEnv,
	)
	if accept {
		warn(msg)
		return nil
	}
	return fmt.Errorf("%s", msg)
}

//go:embed former2-rows.json
var former2RowsJSON []byte

// PinnedFormer2 is the committed derived artifact: the default, no-network
// source buildMapping reads.
var PinnedFormer2 = mustLoadFormer2Artifact()

func mustLoadFormer2Artifact() Former2Artifact {
	var art Former2Artifact
	if err := json.Unmarshal(former2RowsJSON, &art); err != nil {
		panic(fmt.Sprintf("mapping-gen: former2-rows.json does not parse: %v", err))
	}
	return art
}
