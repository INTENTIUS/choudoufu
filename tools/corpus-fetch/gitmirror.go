// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
)

// # Pinning the go-getter half of the corpus (#254)
//
// [modulePins] freezes the registry half: a version listing per package, so
// a ranged constraint resolves to the same release on every run. The other
// 135 of this corpus's 284 non-local module calls are go-getter sources -
// "github.com/org/repo" - and they had no equivalent. go-getter clones a
// branch head, so those calls floated on seven third-party repositories'
// default branches with nothing to pin them to. They were therefore
// installed and then dropped, which left them exactly as unresolved as they
// were before module installation existed.
//
// (Counted 2026-08-16 by walking each resolved corpus entry through its
// local module sources: 284 non-local calls, 149 registry and 135
// go-getter, of which 134 carry no "?ref=". Six repositories are named by
// an entry directly; the seventh is reached only from inside another
// go-getter module and was found by running the installer, which is why
// [installSummary.UnpinnedPackages] reports from the records the installer
// wrote rather than from a census of the corpus's own files.)
//
// This is the pin. live/corpus-manifest.json's "module_sources" names each
// repository and the commit it is frozen at, corpus-fetch builds a bare
// mirror of each under .corpus/_modules, and the module phase runs with
// git's own url.<base>.insteadOf pointing those clone URLs at the mirrors.
// go-getter is unchanged and unaware: it runs the same "git clone
// https://github.com/org/repo.git" it always ran, and git resolves it
// locally.
//
// # Why the mirror is built from commits and never from a remote ref
//
// Every ref the mirror serves is fetched BY SHA into a ref name we choose:
//
//	git fetch --depth 1 origin <commit>:refs/heads/<default branch>
//	git fetch --depth 1 origin <tag commit>:refs/tags/<tag>
//
// so the remote's own ref resolution is never consulted after the pin is
// taken. A repository that force-pushes its default branch, or moves a tag,
// cannot change what this corpus measures - there is nothing here for it to
// move. That is a stronger property than corpus-fetch's own source pins have
// (those resolve a tag and then verify it, which detects a moved tag rather
// than being immune to one), and it is available here only because a mirror
// is built rather than checked out.
//
// # Why the rewrite key carries ".git", and why that is what separates the
// two source kinds
//
// This is the trap that has to be got right. Registry module downloads are
// also git clones - the OpenTofu registry answers
// /v1/modules/<ns>/<name>/<system>/<version>/download with
//
//	{"location": "git::https://github.com/<org>/<repo>?ref=<commit>"}
//
// verified against registry.opentofu.org on 2026-08-16 - so denying git, or
// rewriting all of https://github.com/, would break registry installs too.
//
// What separates them is the ".git" suffix, and it is not a coincidence:
//
//   - go-getter's GitHubDetector rewrites "github.com/org/repo" to
//     "git::https://github.com/org/repo.git", always appending ".git".
//   - the registry's download location carries no ".git" at all.
//
// So the mirror's insteadOf key is the ".git" spelling and only that. A
// registry download for the very same repository is a different string, is
// not rewritten, and goes to the network with its own "?ref=<commit>" pin
// intact.
//
// The suffix also bounds the blast radius, which matters because insteadOf
// is a raw string-prefix replacement rather than a path-component one:
// registering "https://github.com/org/repo" would also capture
// "https://github.com/org/repo-other.git" and send it to "<mirror>-other.git"
// (measured: "git ls-remote --get-url" reports exactly that). ".git" is a
// terminator, so the ".git" spelling cannot capture a sibling repository.
//
// # What is not covered
//
// A source written as a forced bare git URL - "git::https://github.com/
// org/repo", no ".git" - normalizes to a package address this key does not
// match. Such a call is not rewritten, is reported by
// [installSummary.UnpinnedPackages], and has its module record dropped, so
// it cannot float into the measurement unseen. This corpus contains none.
type moduleSourcePin struct {
	// Repo is the clone URL go-getter will hand to git, which for a
	// "github.com/org/repo" source is the ".git" spelling. It doubles as
	// the insteadOf key and as the join back to an installed module
	// record's source address - see [gitCloneURL].
	Repo string `json:"repo"`

	// Commit is the exact commit the mirror's default branch is pinned to,
	// a full 40-character SHA.
	Commit string `json:"commit"`
}

// moduleSourceManifest is corpus-fetch's own view of
// live/corpus-manifest.json. The file is read twice on purpose: once through
// [check.ReadManifest], which owns the definition both corpus-fetch and
// corpus-gen share, and once here for the field only the fetcher needs.
// encoding/json ignores keys a struct does not declare, so neither reader
// constrains the other, and internal/live/check does not grow a concept the
// measurement half has no use for.
type moduleSourceManifest struct {
	ModuleSources []moduleSourcePin `json:"module_sources"`
}

func readModuleSources(path string) ([]moduleSourcePin, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the manifest path, passed by flag
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m moduleSourceManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	for i, pin := range m.ModuleSources {
		switch {
		case pin.Repo == "":
			return nil, fmt.Errorf("%s: module_sources[%d] has no repo", path, i)
		case len(pin.Commit) != 40:
			// The same bar corpus-fetch already holds its source pins to:
			// a short or partial pin is not a pin.
			return nil, fmt.Errorf("%s: module_sources[%d] (%s) commit %q is not a full SHA",
				path, i, pin.Repo, pin.Commit)
		}
	}
	sort.Slice(m.ModuleSources, func(i, j int) bool { return m.ModuleSources[i].Repo < m.ModuleSources[j].Repo })
	return m.ModuleSources, nil
}

// gitMirror is one prepared local mirror and how a module record joins to it.
type gitMirror struct {
	Pin moduleSourcePin

	// Dir is the bare repository the rewrite points at.
	Dir string

	// Rebuilt is true when this run had to build the mirror rather than
	// find it already serving the pin. It is what tells the install pass
	// that anything previously installed from this repository is stale -
	// see [absolutizeSnapshot].
	Rebuilt bool

	// Err is non-nil when the mirror could not be built. The rewrite is
	// registered anyway - see [prepareMirrors].
	Err error
}

// mirrorDir derives a mirror's location from its clone URL, so adding a
// repository to the manifest means adding a repository and nothing else.
func mirrorDir(base, repo string) (string, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return "", fmt.Errorf("parsing %q: %w", repo, err)
	}
	if u.Scheme == "" || u.Path == "" {
		return "", fmt.Errorf("%q is not an absolute clone URL", repo)
	}
	parts := []string{base}
	if u.Host != "" {
		parts = append(parts, u.Host)
	}
	for _, segment := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		// A ".." in a clone URL would put the mirror outside the corpus.
		// The manifest is checked in, so this is a tripwire rather than a
		// defence, but it is a cheap one.
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%q has a path segment that cannot name a directory", repo)
		}
		parts = append(parts, segment)
	}
	return filepath.Join(parts...), nil
}

// gitCloneURL returns the URL go-getter hands to git for a module source
// address, or "" when the source is not a git source.
//
// It goes through [addrs.ParseModuleSource] rather than matching prefixes,
// for the same reason [sourceKind] does: which URL a source resolves to is
// go-getter's detection answer, not this file's guess. The query is stripped
// because go-getter removes "ref", "depth" and "sshkey" from the URL before
// cloning, and the subdir is already split off into ModuleSourceRemote.Subdir.
func gitCloneURL(source string) string {
	addr, err := addrs.ParseModuleSource(source)
	if err != nil {
		return ""
	}
	remote, ok := addr.(addrs.ModuleSourceRemote)
	if !ok {
		return ""
	}
	raw := string(remote.Package)
	if !strings.HasPrefix(raw, "git::") {
		return ""
	}
	raw = strings.TrimPrefix(raw, "git::")
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// prepareMirrors builds or reuses a bare mirror per pinned module source.
//
// A mirror that cannot be built is still returned, with Err set, and its
// rewrite is still registered by [gitRewriteEnv]. That is deliberate: an
// unregistered rewrite would send every call to that repository to the
// network, where it would install a floating branch head and be measured as
// if it were pinned. Registering a rewrite that points at a directory git
// cannot read makes those calls fail instead, once per entry that makes one,
// which the install pass already reports per entry and does not stop for.
func prepareMirrors(ctx context.Context, pins []moduleSourcePin, base string, lock *modulePins, log *os.File) []gitMirror {
	mirrors := make([]gitMirror, 0, len(pins))
	for _, pin := range pins {
		m := gitMirror{Pin: pin}
		dir, err := mirrorDir(base, pin.Repo)
		if err != nil {
			m.Err = err
			mirrors = append(mirrors, m)
			continue
		}
		m.Dir = dir
		rebuilt, err := prepareMirror(ctx, pin, dir, lock, log)
		m.Rebuilt = rebuilt
		if err != nil {
			m.Err = err
			// Leave nothing git can read. A half-built mirror - "git init
			// --bare" ran, the fetch did not - clones successfully and
			// EMPTY, which is the one outcome worse than failing: the
			// module installs as an empty directory and the entry is
			// measured as though the module had no resources in it.
			// Removing it makes every call to this repository fail with
			// "does not appear to be a git repository" instead.
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				m.Err = fmt.Errorf("%w (and the partial mirror could not be removed: %v)", err, rmErr)
			}
		}
		mirrors = append(mirrors, m)
	}
	return mirrors
}

// prepareMirror builds or reuses one mirror. It reports whether it had to
// build, which is what makes anything previously installed from this
// repository stale.
func prepareMirror(ctx context.Context, pin moduleSourcePin, dir string, lock *modulePins, log *os.File) (bool, error) {
	refs, locked := lock.GitMirrors[pin.Repo]
	if !locked || refs.DefaultBranch == "" {
		discovered, err := discoverRefs(ctx, pin.Repo)
		if err != nil {
			return false, err
		}
		refs = discovered
		if lock.GitMirrors == nil {
			lock.GitMirrors = map[string]gitMirrorRefs{}
		}
		lock.GitMirrors[pin.Repo] = refs
	}

	if mirrorIsCurrent(ctx, dir, pin, refs) {
		logf(log, "corpus-fetch: mirror %s already at %s\n", pin.Repo, short(pin.Commit))
		return false, nil
	}

	logf(log, "corpus-fetch: mirror %s -> %s\n", pin.Repo, short(pin.Commit))
	if err := os.RemoveAll(dir); err != nil {
		return true, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a cache directory under the gitignored corpus
		return true, err
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--bare"},
		{"remote", "add", "origin", "--", pin.Repo},
		// By SHA into a ref name we choose, so the remote's own default
		// branch cannot move the pin. See this file's doc comment.
		{"fetch", "--quiet", "--depth", "1", "origin", "--", pin.Commit + ":refs/heads/" + refs.DefaultBranch},
		{"symbolic-ref", "HEAD", "refs/heads/" + refs.DefaultBranch},
	} {
		if err := runGit(ctx, dir, args...); err != nil {
			return true, err
		}
	}

	// Tags second, and non-fatally: a tag is only needed by a call that
	// writes "?ref=<tag>", so a tag that cannot be fetched should cost that
	// one call rather than the whole mirror. A call that then cannot resolve
	// its ref fails in the install pass and is reported against its entry.
	if len(refs.Tags) > 0 {
		names := make([]string, 0, len(refs.Tags))
		for name := range refs.Tags {
			names = append(names, name)
		}
		sort.Strings(names)
		// --no-tags matters: without it git also auto-follows the tag by
		// name, and then refuses the whole fetch with "Cannot fetch both
		// <sha> and refs/tags/<name> to refs/tags/<name>". Fetching by name
		// is the thing this is avoiding, so switching it off is the point
		// rather than a workaround.
		args := []string{"fetch", "--quiet", "--no-tags", "--depth", "1", "origin", "--"}
		for _, name := range names {
			args = append(args, refs.Tags[name]+":refs/tags/"+name)
		}
		if err := runGit(ctx, dir, args...); err != nil {
			logf(log, "corpus-fetch: mirror %s: tags not fetched: %v\n", pin.Repo, err)
		}
	}
	return true, nil
}

// mirrorIsCurrent reports whether an existing mirror already serves exactly
// what the pin and the lock describe, so a warm run does no network work.
func mirrorIsCurrent(ctx context.Context, dir string, pin moduleSourcePin, refs gitMirrorRefs) bool {
	head, err := gitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+refs.DefaultBranch+"^{commit}")
	if err != nil || head != pin.Commit {
		return false
	}
	symref, err := gitOutput(ctx, dir, "symbolic-ref", "HEAD")
	if err != nil || symref != "refs/heads/"+refs.DefaultBranch {
		return false
	}
	for name, want := range refs.Tags {
		at, err := gitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/tags/"+name+"^{commit}")
		if err != nil || at != want {
			return false
		}
	}
	return true
}

// discoverRefs asks the remote once for the names this mirror must serve:
// its default branch, and every tag with the commit that tag pointed at.
// The result is written into the lock and never re-derived, which is what
// makes a "?ref=<tag>" call reproducible rather than dependent on the tag
// still pointing where it did.
func discoverRefs(ctx context.Context, repo string) (gitMirrorRefs, error) {
	refs := gitMirrorRefs{Tags: map[string]string{}}
	out, err := gitOutput(ctx, "", "ls-remote", "--symref", "--", repo)
	if err != nil {
		return refs, fmt.Errorf("listing refs of %s: %w", repo, err)
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "ref:" && len(fields) >= 3 && fields[2] == "HEAD" {
			refs.DefaultBranch = strings.TrimPrefix(fields[1], "refs/heads/")
			continue
		}
		name, ok := strings.CutPrefix(fields[1], "refs/tags/")
		if !ok {
			continue
		}
		// An annotated tag advertises both the tag object and, as
		// "<name>^{}", the commit it peels to. The peeled line wins: the
		// mirror serves a lightweight tag at the commit, which is what
		// go-getter's "rev-parse refs/tags/<name>^{commit}" wants and what
		// a shallow fetch can deliver.
		if peeled, isPeeled := strings.CutSuffix(name, "^{}"); isPeeled {
			refs.Tags[peeled] = fields[0]
			continue
		}
		if _, have := refs.Tags[name]; !have {
			refs.Tags[name] = fields[0]
		}
	}
	if refs.DefaultBranch == "" {
		return refs, fmt.Errorf("%s advertises no HEAD symref", repo)
	}
	return refs, nil
}

// gitRewriteEnv builds the GIT_CONFIG_* environment that points each mirrored
// clone URL at its local mirror.
//
// Environment rather than a config file because it has to be scoped: the same
// process clones the corpus's own sources from github.com before this, and
// writing into anybody's ~/.gitconfig to run a corpus fetch would be
// indefensible. GIT_CONFIG_COUNT is additive over whatever the caller already
// had, so a user's own url rewrites survive alongside these.
func gitRewriteEnv(existing func(string) string, mirrors []gitMirror) map[string]string {
	base := 0
	if n, err := strconv.Atoi(existing("GIT_CONFIG_COUNT")); err == nil && n > 0 {
		base = n
	}
	env := map[string]string{}
	n := base
	for _, m := range mirrors {
		if m.Dir == "" {
			continue
		}
		env["GIT_CONFIG_KEY_"+strconv.Itoa(n)] = "url." + m.Dir + ".insteadOf"
		env["GIT_CONFIG_VALUE_"+strconv.Itoa(n)] = m.Pin.Repo
		n++
	}
	if n == base {
		return nil
	}
	env["GIT_CONFIG_COUNT"] = strconv.Itoa(n)
	return env
}

// withEnv sets vars for the duration of the returned function's lifetime and
// restores exactly what was there before, including the difference between
// "set to empty" and "unset".
func withEnv(vars map[string]string) func() {
	type prior struct {
		value string
		set   bool
	}
	saved := map[string]prior{}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, set := os.LookupEnv(key)
		saved[key] = prior{value: value, set: set}
		os.Setenv(key, vars[key]) //nolint:errcheck // setting a well-formed name cannot fail
	}
	return func() {
		for key, was := range saved {
			if was.set {
				os.Setenv(key, was.value) //nolint:errcheck // restoring what was read
				continue
			}
			os.Unsetenv(key) //nolint:errcheck // restoring what was read
		}
	}
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // arguments come from the checked-in manifest and lock
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // arguments come from the checked-in manifest and lock
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
