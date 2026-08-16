// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package pluginschema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestCopiesRetryTheSameTransientFailure guards against the exact shape of
// issue #222's regression: tools/survey-gen/schemas.go and
// tools/estate-gen/schemas.go each carry a verbatim copy of this package's
// Acquire, made before this package existed (see this package's doc
// comment). When a go-plugin handshake failure across ~75 back-to-back
// subprocess spawns turned out to need a bounded retry, the fix landed here
// and was hand-copied into survey-gen's copy - its own comment says so
// ("Copied from internal/live/pluginschema.isTransientLaunchError ... kept
// in step by hand") - but never reached estate-gen's copy, which still calls
// GetProviderSchema once with no retry at all.
//
// This is deliberately anchored to acquire.go's own literal error string
// rather than a value restated by hand in this test: acquire.go is the
// production code every corpus-gen run actually exercises, not a fixture
// kept in sync by convention, so a copy missing the same literal has
// provably not been kept in step with the code that motivated it.
func TestCopiesRetryTheSameTransientFailure(t *testing.T) {
	root, err := repoRootForTest()
	if err != nil {
		t.Fatal(err)
	}

	acquireSrc, err := os.ReadFile(filepath.Join(root, "internal", "live", "pluginschema", "acquire.go"))
	if err != nil {
		t.Fatal(err)
	}

	sigPattern := regexp.MustCompile(`strings\.Contains\(err\.Error\(\), "([^"]+)"\)`)
	m := sigPattern.FindSubmatch(acquireSrc)
	if m == nil {
		t.Fatalf("acquire.go's isTransientLaunchError no longer matches strings.Contains(err.Error(), %q) - update this test's extraction pattern to match its new shape", "...")
	}
	signature := string(m[1])
	if signature == "" {
		t.Fatal("extracted an empty transient-failure signature from acquire.go")
	}

	// A bounded retry loop, not just detection: the fix is retrying the
	// launch, not merely recognizing the failure.
	retryPattern := regexp.MustCompile(`(?s)for attempt := 1; attempt <= maxAttempts;.*isTransientLaunchError`)

	copies := []string{
		filepath.Join(root, "tools", "survey-gen", "schemas.go"),
		filepath.Join(root, "tools", "estate-gen", "schemas.go"),
	}
	for _, path := range copies {
		rel, _ := filepath.Rel(root, path)
		src, err := os.ReadFile(path) //nolint:gosec // fixed paths under the checkout
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if !containsString(src, signature) {
			t.Errorf("%s does not contain acquire.go's transient-launch-failure signature %q - "+
				"this copy has fallen out of step with internal/live/pluginschema.Acquire's retry fix "+
				"(issue #222) and will silently miss a schema on a subprocess launch flake", rel, signature)
			continue
		}
		if !retryPattern.Match(src) {
			t.Errorf("%s recognizes the transient-launch signature but does not retry the launch in a "+
				"bounded loop (`for attempt := 1; attempt <= maxAttempts` calling isTransientLaunchError) - "+
				"detection without retry does not fix issue #222", rel)
		}
	}
}

func containsString(src []byte, needle string) bool {
	return regexp.MustCompile(regexp.QuoteMeta(needle)).Match(src)
}

// repoRootForTest resolves the repository root from this test file's own
// location, the same approach tools/survey-gen/main.go's repoRoot uses.
func repoRootForTest() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at internal/live/pluginschema/duplication_test.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
