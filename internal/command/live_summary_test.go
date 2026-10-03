// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const liveSummarySet = `{
  "roots": [
    {"root": "roots/acme", "estate": "acme", "status": "planned", "plan": {"format_version": "1.2", "resource_changes": [
      {"address": "aws_s3_bucket.logs", "change": {"actions": ["create"], "after": {"bucket": "acme-logs"}}}]}},
    {"root": "roots/globex", "estate": "globex", "status": "planned", "plan": {"format_version": "1.2", "resource_changes": [
      {"address": "aws_s3_bucket.logs", "change": {"actions": ["create"], "after": {"bucket": "globex-logs"}}},
      {"address": "aws_sqs_queue.old", "change": {"actions": ["delete"], "before": {"id": "q"}}}]}},
    {"root": "roots/initech", "estate": "initech", "status": "planned", "plan": {"format_version": "1.2", "resource_changes": [
      {"address": "aws_s3_bucket.logs", "change": {"actions": ["create"], "after": {"bucket": "initech-logs"}}}]}},
    {"root": "roots/hooli", "estate": "hooli", "status": "failed", "error": "Error: no valid credential sources"}
  ]
}`

func runLiveSummary(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	view, done := testView(t)
	c := &LiveSummaryCommand{Meta: Meta{View: view}, stdin: strings.NewReader(stdin)}
	code := c.Run(args)
	out := done(t)
	return code, out.Stdout(), out.Stderr()
}

func TestLiveSummaryCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.json")
	if err := os.WriteFile(path, []byte(liveSummarySet), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("text from a file", func(t *testing.T) {
		code, stdout, stderr := runLiveSummary(t, "", path)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		for _, want := range []string{
			"4 roots: 2 groups, 1 failed, 1 destroy or replace.",
			"roots/hooli (estate hooli): Error: no valid credential sources",
			"Group 1: 2 roots, identical change:",
			"roots: roots/acme, roots/initech",
			"Group 2: 1 root (outlier), group 1's change plus:",
			"roots/globex: aws_sqs_queue.old (delete)",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("json from stdin", func(t *testing.T) {
		code, stdout, stderr := runLiveSummary(t, liveSummarySet, "-json", "-")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		var doc struct {
			Kind   string `json:"kind"`
			Groups []struct {
				Members []string `json:"members"`
			} `json:"groups"`
			Failed []struct {
				Root string `json:"root"`
			} `json:"failed"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("not JSON: %s\n%s", err, stdout)
		}
		if doc.Kind != "set" || len(doc.Groups) != 2 || len(doc.Failed) != 1 || doc.Failed[0].Root != "roots/hooli" {
			t.Errorf("unexpected document: %+v", doc)
		}
	})

	t.Run("markdown under a limit", func(t *testing.T) {
		code, stdout, stderr := runLiveSummary(t, "", "-markdown", "-markdown-limit=420", path)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "**Truncated:** this note leaves out") || len([]rune(stdout)) > 421 {
			t.Errorf("markdown was not cut to 420 characters with a notice (%d):\n%s", len([]rune(stdout)), stdout)
		}
	})

	t.Run("a document that is neither", func(t *testing.T) {
		code, _, stderr := runLiveSummary(t, `{"values": {}}`, "-")
		if code != 1 || !strings.Contains(stderr, "Not a set document or a plan") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
	})

	t.Run("two arguments", func(t *testing.T) {
		code, _, stderr := runLiveSummary(t, "", path, path)
		if code != 1 || !strings.Contains(stderr, "Wrong number of arguments") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
	})
}
