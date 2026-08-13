// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureRepo builds a minimal checkout under a temp dir: just the
// files DETECT reads (live/survey.json, live/registry.json,
// tools/registry-gen/pinned-types.json), so Detect's pure comparison logic
// runs against fixture pins with no real checkout and no network.
func writeFixtureRepo(t *testing.T, providerVersion, registryDigest string, registryResources int, pinnedTypes []string) string {
	t.Helper()
	root := t.TempDir()

	mustWriteJSON(t, filepath.Join(root, "live", "survey.json"), map[string]any{
		"provider":         "hashicorp/aws",
		"provider_version": providerVersion,
	})
	mustWriteJSON(t, filepath.Join(root, "live", "registry.json"), map[string]any{
		"pin": map[string]any{
			"digest":    registryDigest,
			"resources": registryResources,
			"accepted":  "2026-01-01",
		},
	})
	mustWriteJSON(t, filepath.Join(root, "tools", "registry-gen", "pinned-types.json"), pinnedTypes)

	return root
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling fixture for %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating fixture directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", path, err)
	}
}

func buildFixtureZip(t *testing.T, schemas map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for typeName, body := range schemas {
		f, err := w.Create("schemas/" + typeName + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func schemaJSON(typeName string) string {
	return `{"typeName":"` + typeName + `","description":"fixture"}`
}

func fetchStatic(urlToBody map[string][]byte) httpGetter {
	return func(_ context.Context, url string) ([]byte, error) {
		body, ok := urlToBody[url]
		if !ok {
			return nil, errors.New("unexpected URL in test: " + url)
		}
		return body, nil
	}
}

func TestLatestStableVersion(t *testing.T) {
	body := []byte(`{"versions":[
		{"version":"6.58.0"},
		{"version":"6.59.0"},
		{"version":"7.0.0-beta1"},
		{"version":"6.6.0"}
	]}`)
	got, err := latestStableVersion(body)
	if err != nil {
		t.Fatalf("latestStableVersion: %v", err)
	}
	if got != "6.59.0" {
		t.Errorf("latestStableVersion = %q, want 6.59.0 (prereleases excluded, 6.6.0 < 6.59.0)", got)
	}
}

func TestLatestStableVersion_NoStableVersions(t *testing.T) {
	body := []byte(`{"versions":[{"version":"7.0.0-beta1"}]}`)
	if _, err := latestStableVersion(body); err == nil {
		t.Error("latestStableVersion with only prereleases: want an error, got nil")
	}
}

func TestSemverLess(t *testing.T) {
	cases := []struct {
		a, b [3]int
		want bool
	}{
		{[3]int{6, 58, 0}, [3]int{6, 59, 0}, true},
		{[3]int{6, 59, 0}, [3]int{6, 58, 0}, false},
		{[3]int{6, 58, 0}, [3]int{6, 58, 0}, false},
		{[3]int{6, 9, 0}, [3]int{6, 58, 0}, true}, // numeric, not lexicographic
	}
	for _, c := range cases {
		if got := semverLess(c.a, c.b); got != c.want {
			t.Errorf("semverLess(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDetectProvider_NoDrift(t *testing.T) {
	root := writeFixtureRepo(t, "6.58.0", "sha256:x", 1, []string{"AWS::S3::Bucket"})
	fetch := fetchStatic(map[string][]byte{
		providerVersionsURL: []byte(`{"versions":[{"version":"6.58.0"}]}`),
	})

	got, err := detectProvider(context.Background(), root, fetch)
	if err != nil {
		t.Fatalf("detectProvider: %v", err)
	}
	if got.Drifted {
		t.Errorf("detectProvider.Drifted = true, want false (pinned == current)")
	}
	if got.Pinned != "6.58.0" || got.Current != "6.58.0" {
		t.Errorf("detectProvider = %+v, want pinned=current=6.58.0", got)
	}
}

func TestDetectProvider_Drift(t *testing.T) {
	root := writeFixtureRepo(t, "6.58.0", "sha256:x", 1, []string{"AWS::S3::Bucket"})
	fetch := fetchStatic(map[string][]byte{
		providerVersionsURL: []byte(`{"versions":[{"version":"6.58.0"},{"version":"6.59.0"}]}`),
	})

	got, err := detectProvider(context.Background(), root, fetch)
	if err != nil {
		t.Fatalf("detectProvider: %v", err)
	}
	if !got.Drifted {
		t.Error("detectProvider.Drifted = false, want true (6.59.0 available, 6.58.0 pinned)")
	}
	if got.Current != "6.59.0" {
		t.Errorf("detectProvider.Current = %q, want 6.59.0", got.Current)
	}
}

func TestDetectRegistry_NoDrift(t *testing.T) {
	schemas := map[string]string{"AWS::S3::Bucket": schemaJSON("AWS::S3::Bucket")}
	zipData := buildFixtureZip(t, schemas)
	digest := registryContentDigest(map[string][]byte{"AWS::S3::Bucket": []byte(schemaJSON("AWS::S3::Bucket"))})

	root := writeFixtureRepo(t, "6.58.0", digest, 1, []string{"AWS::S3::Bucket"})
	fetch := fetchStatic(map[string][]byte{registryZipURL: zipData})

	got, err := detectRegistry(context.Background(), root, fetch)
	if err != nil {
		t.Fatalf("detectRegistry: %v", err)
	}
	if got.Drifted {
		t.Errorf("detectRegistry.Drifted = true, want false: %+v", got)
	}
}

func TestDetectRegistry_ResourceSetMoved(t *testing.T) {
	root := writeFixtureRepo(t, "6.58.0", "sha256:stale", 1, []string{"AWS::S3::Bucket"})
	zipData := buildFixtureZip(t, map[string]string{
		"AWS::S3::Bucket": schemaJSON("AWS::S3::Bucket"),
		"AWS::EC2::VPC":   schemaJSON("AWS::EC2::VPC"), // added, not in pinned-types.json
	})
	fetch := fetchStatic(map[string][]byte{registryZipURL: zipData})

	got, err := detectRegistry(context.Background(), root, fetch)
	if err != nil {
		t.Fatalf("detectRegistry: %v", err)
	}
	if !got.Drifted {
		t.Fatal("detectRegistry.Drifted = false, want true (digest moved)")
	}
	if got.Detail == "" || !strings.Contains(got.Detail, "resource set moved") {
		t.Errorf("detectRegistry.Detail = %q, want it to say the resource set moved", got.Detail)
	}
}

func TestDetectRegistry_ByteOnlyDrift(t *testing.T) {
	pinnedSchema := schemaJSON("AWS::S3::Bucket")
	digest := registryContentDigest(map[string][]byte{"AWS::S3::Bucket": []byte(pinnedSchema)})
	root := writeFixtureRepo(t, "6.58.0", digest, 1, []string{"AWS::S3::Bucket"})

	// Same type set, different bytes (a description edit upstream).
	editedSchema := `{"typeName":"AWS::S3::Bucket","description":"edited"}`
	zipData := buildFixtureZip(t, map[string]string{"AWS::S3::Bucket": editedSchema})
	fetch := fetchStatic(map[string][]byte{registryZipURL: zipData})

	got, err := detectRegistry(context.Background(), root, fetch)
	if err != nil {
		t.Fatalf("detectRegistry: %v", err)
	}
	if !got.Drifted {
		t.Fatal("detectRegistry.Drifted = false, want true (digest moved)")
	}
	if !strings.Contains(got.Detail, "byte-only drift") {
		t.Errorf("detectRegistry.Detail = %q, want it to say byte-only drift", got.Detail)
	}
}

func TestDriftReport_Any(t *testing.T) {
	none := DriftReport{Provider: PinState{Drifted: false}, Registry: PinState{Drifted: false}}
	if none.Any() {
		t.Error("DriftReport.Any() = true for two non-drifted pins, want false")
	}
	one := DriftReport{Provider: PinState{Drifted: true}, Registry: PinState{Drifted: false}}
	if !one.Any() {
		t.Error("DriftReport.Any() = false with Provider drifted, want true")
	}
}
