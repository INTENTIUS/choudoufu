// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command override-gen writes live/overrides.json, the table of provider
// behaviour overrides (GitHub issue #2309), from internal/live/override's
// registry. Run it from the repository root after adding or changing an
// override; internal/live/override's TestGeneratedTableIsCurrent fails
// until it has been run.
//
// With -release-notes REF it writes nothing and prints a CHANGELOG fragment
// instead: one line per override added since REF (the previous release
// tag), with what it fixes, the upstream issue in a code span, the upstream
// people it credits by bare login (an "@" would notify them) and the
// choudoufu commit that added it.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/intentius/choudoufu/internal/live/override"
)

func main() {
	since := flag.String("release-notes", "", "print release notes for overrides added since this git ref, and write nothing")
	flag.Parse()
	if *since != "" {
		if err := releaseNotes(*since); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	out, err := override.TableJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(override.TablePath, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d overrides\n", override.TablePath, len(override.All()))
}

func releaseNotes(since string) error {
	if _, err := git("rev-parse", "--verify", since+"^{commit}"); err != nil {
		return fmt.Errorf("-release-notes %s: not a commit: %w", since, err)
	}
	var lines []string
	for _, o := range override.All() {
		sha, err := addedIn(o.Name)
		if err != nil {
			return err
		}
		if _, err := git("merge-base", "--is-ancestor", sha, since); err == nil {
			continue // released already
		}
		lines = append(lines, noteLine(o, sha))
	}
	if len(lines) == 0 {
		fmt.Printf("No provider overrides added since %s.\n", since)
		return nil
	}
	fmt.Println("PROVIDER OVERRIDES:")
	fmt.Println()
	fmt.Printf("On by default; `%s=-name` turns one off, `%s=off` all of them (#2309).\n\n", override.EnvVar, override.EnvVar)
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// noteLine is one override's release-note entry. The upstream reference is
// in a code span so that the release page does not link it.
func noteLine(o override.Override, sha string) string {
	var thanks []string
	for _, c := range o.Credits {
		thanks = append(thanks, c.Login+" ("+c.For+")")
	}
	line := fmt.Sprintf("- `%s`: %s. Stands in for `%s`.", o.Name, o.Summary, o.Upstream)
	if len(thanks) > 0 {
		line += " Thanks to " + strings.Join(thanks, " and ") + "."
	}
	return line + fmt.Sprintf(" Added in %s.", sha)
}

// addedIn is the first commit that introduced the override's name into the
// override package.
func addedIn(name string) (string, error) {
	out, err := git("log", "--reverse", "--format=%h", "-S", `"`+name+`"`, "--", "internal/live/override/")
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if first == "" {
		return "", fmt.Errorf("no commit adds the override %q; commit it first", name)
	}
	return first, nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
