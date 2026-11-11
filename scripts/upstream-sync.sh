#!/usr/bin/env bash
# scripts/upstream-sync.sh: bring an upstream OpenTofu ref into choudoufu
# (#2335, unit U0 of #2256). It does in one command what #1778 (v1.13.0) and
# #2323 (v1.13.2) did by hand.
#
# Usage, from a worktree on a branch off main (never the primary checkout):
#
#   scripts/upstream-sync.sh [options] <upstream-ref>
#       Graft, rewrite twins, merge. A clean merge is committed and re-pinned
#       on the current branch. On conflicts it lists them by kind and stops
#       (exit 2, E_CONFLICTS) without touching the worktree.
#   scripts/upstream-sync.sh --materialize [options] <upstream-ref>
#       Same, but writes the merge result into the worktree and index
#       (conflict markers in files, unmerged stages in the index, so
#       `git status` and `git diff` work as in a normal merge) and stops.
#       Resolve, `git add` the resolutions, then:
#   scripts/upstream-sync.sh --finish
#       Commits the staged tree as a merge of HEAD and the raw graft, then
#       re-pins and regenerates live/fork-surface.json in a second commit.
#   scripts/upstream-sync.sh --abort
#       Drops a materialized sync: resets index and worktree to HEAD.
#   scripts/upstream-sync.sh --graft-only [options] <upstream-ref>
#       Step 1 only. Prints the graft commit's sha. Changes nothing else.
#   scripts/upstream-sync.sh --report
#       Step 5 only: the state of every upstream issue in the gap table.
#
# Options:
#   --prev-graft <commit>  Previous graft. Default: the most recent commit
#                          reachable from HEAD whose subject starts with
#                          "graft: OpenTofu" or "upstream: OpenTofu".
#   --no-fetch             Don't `git fetch upstream` first.
#   --no-report            Skip step 5 (it reads the GitHub API).
#
# <upstream-ref> is an upstream branch name (resolved as upstream/<ref>), a
# tag such as v1.13.2, or a commit sha.
#
# Steps (#2335):
#   1. Graft. The upstream ref's tree, unmodified, committed with the
#      previous graft as its only parent. The graft chain stays raw upstream
#      trees, so any graft compares byte for byte with upstream. The commit
#      uses the upstream commit's date, so grafting the same ref onto the
#      same previous graft gives the same tree and parent every time. A
#      local branch sync/<upstream-short>-graft points at it.
#   2. Rewrite. Throwaway twins of the previous graft and the new graft with
#      exactly these changes and nothing else: github.com/opentofu/opentofu
#      -> github.com/intentius/choudoufu in .go files and go.mod files,
#      cmd/tofu renamed to cmd/choudoufu, website/ rfc/ .github/workflows/
#      deleted. Each twin is checked afterwards; a twin that still carries
#      any of those is E_REWRITE_INCOMPLETE.
#   3. Merge. git merge-tree --write-tree --merge-base=<prev twin> HEAD
#      <new twin>. The commit's parents are HEAD and the raw graft, never
#      the twin. Never squashed or rebased.
#   4. Re-pin forkPointCommit, forkSurfaceForkPoint, upstreamBaseCommit,
#      changelogPastBases and the README's base lines, then regenerate
#      live/fork-surface.json (go run ./tools/forkdiff-gen, then -render).
#      version/VERSION is never edited: it arrives through the merge, and
#      --finish refuses a tree whose version/VERSION differs from upstream's
#      (E_VERSION_EDITED).
#   5. Report the state of each upstream issue in the gap table. Read-only
#      GET requests; nothing is written upstream.
#
# Running it again at a ref that is already merged changes nothing and
# exits 0.
#
# Named errors (stderr "upstream-sync: error E_...: ..."), exit 1 unless noted:
#   E_USAGE, E_NO_UPSTREAM_REMOTE, E_BAD_REF, E_UPSTREAM_MISSING,
#   E_NO_PREV_GRAFT, E_WRONG_PREV_GRAFT, E_REWRITE_INCOMPLETE,
#   E_CONFLICTS (exit 2), E_ON_MAIN, E_DIRTY_WORKTREE, E_SYNC_IN_PROGRESS,
#   E_NO_SYNC_IN_PROGRESS, E_HEAD_MOVED, E_UNRESOLVED, E_MARKERS_LEFT,
#   E_UNSTAGED, E_VERSION_EDITED, E_REPIN_PATTERN, E_REGENERATE, E_GIT.
#
# Test hook, for scripts/upstream-sync-test.sh only: UPSTREAM_SYNC_SKIP_REWRITE=1
# skips step 2 (twins are the raw trees). The twin check must then fail with
# E_REWRITE_INCOMPLETE; that is the red case proving the check can fail.
#
# Gap table source: see gap_upstream_numbers below. Set
# UPSTREAM_SYNC_GAP_FILE to a file to read the numbers from it instead
# (every `#NNNN` code span in it), which is where U2 repoints it.

set -euo pipefail

MODULE_OLD='github.com/opentofu/opentofu'
MODULE_NEW='github.com/intentius/choudoufu'
UPSTREAM_REMOTE=upstream
UPSTREAM_REPO=opentofu/opentofu
GRAFT_SUBJECT_RE='^(graft|upstream): OpenTofu '

die() {
	local code=$1
	shift
	echo "upstream-sync: error $code: $*" >&2
	if [ "$code" = E_CONFLICTS ]; then exit 2; fi
	exit 1
}
say() { echo "upstream-sync: $*"; }

# gap_upstream_numbers prints the upstream issue and PR numbers named in the
# gap table, one per line. Until U2 (#2337) moves the table into the repo it
# lives in #2256's body, and this list is copied from it (2026-11-11).
gap_upstream_numbers() {
	if [ -n "${UPSTREAM_SYNC_GAP_FILE:-}" ]; then
		[ -r "$UPSTREAM_SYNC_GAP_FILE" ] || die E_USAGE "UPSTREAM_SYNC_GAP_FILE=$UPSTREAM_SYNC_GAP_FILE is not readable"
		grep -oE '`#[0-9]+`' "$UPSTREAM_SYNC_GAP_FILE" | tr -d '`#' | awk '!seen[$0]++'
		return
	fi
	printf '%s\n' 4714 3472 4058 4740 4686 4693 4682 4390 4360 4697 4741 4695 4688 4746 4768 4691 4687 4726 4727 4745 4683
}

# ---------------------------------------------------------------- arguments

mode=sync
ref=
prev_graft_arg=
do_fetch=1
do_report=1
while [ $# -gt 0 ]; do
	case $1 in
	--materialize) mode=materialize ;;
	--finish) mode=finish ;;
	--abort) mode=abort ;;
	--graft-only) mode="graft-only" ;;
	--report) mode=report ;;
	--prev-graft)
		[ $# -ge 2 ] || die E_USAGE "--prev-graft needs a commit"
		prev_graft_arg=$2
		shift
		;;
	--prev-graft=*) prev_graft_arg=${1#*=} ;;
	--no-fetch) do_fetch=0 ;;
	--no-report) do_report=0 ;;
	-h | --help)
		sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*) die E_USAGE "unknown option $1" ;;
	*)
		[ -z "$ref" ] || die E_USAGE "one upstream ref only (got $ref and $1)"
		ref=$1
		;;
	esac
	shift
done

case $mode in
sync | materialize | graft-only) [ -n "$ref" ] || die E_USAGE "an upstream ref is required (e.g. main, v1.13.2, a sha)" ;;
*) [ -z "$ref" ] || die E_USAGE "--$mode takes no upstream ref" ;;
esac

top=$(git rev-parse --show-toplevel) || die E_GIT "not in a git checkout"
cd "$top"
state_dir=$(git rev-parse --git-path upstream-sync)
case $state_dir in /*) ;; *) state_dir=$top/$state_dir ;; esac
state_file=$state_dir/state
tmp=$(mktemp -d "${TMPDIR:-/tmp}/upstream-sync.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

# ---------------------------------------------------------------- helpers

short() { git rev-parse --short=10 "$1"; }
tree_of() { git rev-parse "$1^{tree}"; }

require_branch_off_main() {
	local b
	b=$(git symbolic-ref -q --short HEAD) || die E_ON_MAIN "HEAD is detached; check out a branch off main first"
	[ "$b" != main ] || die E_ON_MAIN "refusing to commit on main; run from a branch off main"
}

require_clean_worktree() {
	[ -z "$(git status --porcelain)" ] || die E_DIRTY_WORKTREE "worktree has changes or untracked files; commit or stash them first"
}

check_upstream_remote() {
	local url
	url=$(git remote get-url "$UPSTREAM_REMOTE" 2>/dev/null) || die E_NO_UPSTREAM_REMOTE "no '$UPSTREAM_REMOTE' remote; add it: git remote add $UPSTREAM_REMOTE https://github.com/$UPSTREAM_REPO"
	case $url in
	*"$UPSTREAM_REPO"*) ;;
	*) die E_NO_UPSTREAM_REMOTE "remote '$UPSTREAM_REMOTE' is $url, not $UPSTREAM_REPO" ;;
	esac
}

resolve_upstream_ref() {
	local r=$1 c
	if c=$(git rev-parse -q --verify "refs/remotes/$UPSTREAM_REMOTE/$r^{commit}" 2>/dev/null); then echo "$c"; return; fi
	if c=$(git rev-parse -q --verify "refs/tags/$r^{commit}" 2>/dev/null); then echo "$c"; return; fi
	if [[ $r =~ ^[0-9a-f]{7,40}$ ]] && c=$(git rev-parse -q --verify "$r^{commit}" 2>/dev/null); then echo "$c"; return; fi
	die E_BAD_REF "cannot resolve upstream ref '$r' (tried $UPSTREAM_REMOTE/$r, tag $r, sha); try without --no-fetch"
}

# graft_claim prints the full upstream sha a graft commit claims to carry:
# its Upstream-Commit trailer, else the first parenthesized hex in its
# subject (93bb2800b4 "graft: OpenTofu v1.13.2 tree (765062032c) ...",
# 2958e54f45 "upstream: OpenTofu v1.13.0 (2b6193043d) tree ...").
graft_claim() {
	local g=$1 claim subject
	claim=$(git log -1 --format='%(trailers:key=Upstream-Commit,valueonly)' "$g" | head -n1 | tr -d '[:space:]')
	if [ -z "$claim" ]; then
		subject=$(git log -1 --format=%s "$g")
		claim=$(printf '%s\n' "$subject" | grep -oE '\([0-9a-f]{7,40}\)' | head -n1 | tr -d '()' || true)
	fi
	[ -n "$claim" ] || return 1
	echo "$claim"
}

# claim_commit resolves a graft's claim to a full sha, or dies.
claim_commit() {
	local g=$1 claim
	claim=$(graft_claim "$g") || die E_WRONG_PREV_GRAFT "$(short "$g") names no upstream commit in its subject or an Upstream-Commit trailer"
	git rev-parse -q --verify "$claim^{commit}" 2>/dev/null || die E_UPSTREAM_MISSING "graft $(short "$g") claims upstream $claim, which is not in this object database; run: git fetch $UPSTREAM_REMOTE"
}

latest_graft() {
	git log --format='%H%x09%s' HEAD | awk -F '\t' -v re="$GRAFT_SUBJECT_RE" '$2 ~ re { print $1; exit }' || true
}

# validate_prev_graft dies with E_WRONG_PREV_GRAFT unless $1 is a graft on
# HEAD's ancestry whose tree is byte-identical to the upstream commit it
# claims. When $2 is "latest", it must also be the most recent graft on
# HEAD: merging against an older one would use a stale merge base.
validate_prev_graft() {
	local g=$1 need_latest=$2 subject claim latest
	g=$(git rev-parse -q --verify "$g^{commit}" 2>/dev/null) || die E_WRONG_PREV_GRAFT "'$1' is not a commit"
	git merge-base --is-ancestor "$g" HEAD || die E_WRONG_PREV_GRAFT "$(short "$g") is not an ancestor of HEAD"
	subject=$(git log -1 --format=%s "$g")
	[[ $subject =~ $GRAFT_SUBJECT_RE ]] || die E_WRONG_PREV_GRAFT "$(short "$g") is not a graft commit (subject: $subject)"
	claim=$(claim_commit "$g")
	[ "$(tree_of "$g")" = "$(tree_of "$claim")" ] || die E_WRONG_PREV_GRAFT "$(short "$g")'s tree differs from upstream $(short "$claim"), the commit it claims to carry"
	if [ "$need_latest" = latest ]; then
		latest=$(latest_graft)
		[ "$latest" = "$g" ] || die E_WRONG_PREV_GRAFT "$(short "$g") is not the most recent graft on HEAD ($(short "$latest") is); merging against it would use a stale merge base"
	fi
	echo "$g"
}

upstream_label() {
	local c=$1 t
	if t=$(git describe --tags --exact-match --match 'v[0-9]*' "$c" 2>/dev/null); then echo "$t"; return; fi
	git show "$c:version/VERSION" | tr -d '[:space:]'
}

# make_graft prints the graft commit for upstream $1 on previous graft $2.
make_graft() {
	local up=$1 prev=$2 label date msg
	label=$(upstream_label "$up")
	date=$(git log -1 --format=%cI "$up")
	msg=$tmp/graft-msg
	{
		echo "graft: OpenTofu $label tree ($(short "$up")) on graft $(short "$prev")"
		echo
		echo "Upstream $UPSTREAM_REPO commit $(short "$up"), its tree committed"
		echo "unmodified onto the previous graft $(short "$prev") by scripts/upstream-sync.sh,"
		echo "so the graft chain stays raw upstream trees."
		echo
		echo "Upstream-Commit: $up"
	} >"$msg"
	GIT_AUTHOR_DATE=$date GIT_COMMITTER_DATE=$date git commit-tree "$(tree_of "$up")" -p "$prev" -F "$msg" ||
		die E_GIT "git commit-tree failed for the graft"
}

# build_twin prints a throwaway commit whose tree is $1's with step 2's
# rewrite applied, and nothing else.
build_twin() {
	local src=$1 name=$2 idx tree
	if [ "${UPSTREAM_SYNC_SKIP_REWRITE:-}" = 1 ]; then
		say "UPSTREAM_SYNC_SKIP_REWRITE=1: twin $name is the raw tree (test hook)" >&2
		tree=$(tree_of "$src")
	else
		idx=$tmp/index.$name
		mkdir -p "$tmp/blobs.$name"
		GIT_INDEX_FILE=$idx perl -e '
use strict; use warnings; use IPC::Open2;
my ($src, $dir, $old, $new) = @ARGV;
my @e;
{
	local $/ = "\0";
	open(my $ls, "-|", "git", "ls-tree", "-r", "-z", "--full-tree", $src) or die "ls-tree: $!";
	while (my $r = <$ls>) {
		chomp $r;
		my ($meta, $path) = split /\t/, $r, 2;
		my ($mode, $type, $sha) = split / /, $meta;
		next if $path =~ m{^(?:website|rfc|\.github/workflows)/};
		$path =~ s{^cmd/tofu/}{cmd/choudoufu/};
		push @e, [$mode, $type, $sha, $path];
	}
	close $ls or die "ls-tree failed\n";
}
my $pid = open2(my $out, my $in, "git", "cat-file", "--batch");
binmode $out; binmode $in;
my (@files, @which);
for my $i (0 .. $#e) {
	my ($mode, $type, $sha, $path) = @{$e[$i]};
	next unless $type eq "blob" && $mode ne "120000";
	next unless $path =~ /\.go\z/ || $path =~ m{(?:\A|/)go\.mod\z};
	print $in "$sha\n"; $in->flush;
	my $hdr = <$out>; defined $hdr or die "cat-file: no header for $sha\n";
	my (undef, undef, $size) = split / /, $hdr;
	my $data = ""; my $got = 0;
	while ($got < $size) { my $n = read($out, $data, $size - $got, $got); die "cat-file: short read\n" unless $n; $got += $n; }
	my $nl; read($out, $nl, 1);
	next if index($data, $old) < 0;
	$data =~ s/\Q$old\E/$new/g;
	my $f = "$dir/" . scalar(@files);
	open(my $fh, ">:raw", $f) or die "$f: $!"; print $fh $data; close $fh or die "$f: $!";
	push @files, $f; push @which, $i;
}
close $in; waitpid $pid, 0;
if (@files) {
	open(my $lf, ">", "$dir.list") or die; print $lf "$_\n" for @files; close $lf;
	my @shas = split /\n/, `git hash-object -w --no-filters --stdin-paths < "$dir.list"`;
	die "hash-object failed\n" if $? || @shas != @files;
	$e[$which[$_]][2] = $shas[$_] for 0 .. $#files;
}
open(my $ui, "|-", "git", "update-index", "-z", "--index-info") or die;
print $ui "$_->[0] $_->[2]\t$_->[3]\0" for @e;
close $ui or die "update-index failed\n";
' "$src" "$tmp/blobs.$name" "$MODULE_OLD" "$MODULE_NEW" || die E_GIT "rewriting twin $name failed"
		tree=$(GIT_INDEX_FILE=$idx git write-tree) || die E_GIT "write-tree for twin $name failed"
	fi
	check_twin "$src" "$tree" "$name"
	GIT_AUTHOR_NAME=upstream-sync GIT_AUTHOR_EMAIL=upstream-sync@invalid GIT_AUTHOR_DATE='@0 +0000' \
		GIT_COMMITTER_NAME=upstream-sync GIT_COMMITTER_EMAIL=upstream-sync@invalid GIT_COMMITTER_DATE='@0 +0000' \
		git commit-tree --no-gpg-sign "$tree" -m "upstream-sync twin ($name) of $(short "$src")" || die E_GIT "commit-tree for twin $name failed"
}

# check_twin is the guard that step 2 ran: a twin must carry none of what
# the rewrite removes. It reads the twin tree, not the code that made it.
check_twin() {
	local src=$1 tree=$2 name=$3 bad=
	if [ -n "$(git ls-tree "$tree" -- website rfc)" ] || [ -n "$(git ls-tree "$tree" -- .github/workflows)" ]; then
		bad="$bad website/, rfc/ or .github/workflows/ still present;"
	fi
	if [ -n "$(git ls-tree "$tree" -- cmd/tofu)" ]; then
		bad="$bad cmd/tofu still present;"
	fi
	if [ -n "$(git ls-tree "$(tree_of "$src")" -- cmd/tofu)" ] && [ -z "$(git ls-tree "$tree" -- cmd/choudoufu)" ]; then
		bad="$bad cmd/choudoufu missing;"
	fi
	local left
	left=$(git grep -l -F "$MODULE_OLD" "$tree" -- '*.go' 'go.mod' '*/go.mod' | wc -l | tr -d ' ' || true)
	if [ "$left" != 0 ]; then
		bad="$bad $left .go/go.mod files still name $MODULE_OLD;"
	fi
	[ -z "$bad" ] || die E_REWRITE_INCOMPLETE "twin $name of $(short "$src"):$bad step 2 did not run or was incomplete"
}

write_state() {
	mkdir -p "$state_dir"
	{
		echo "head=$1"
		echo "graft=$2"
		echo "upstream=$3"
		echo "ref=$4"
		echo "prev_graft=$5"
		echo "tree=$6"
	} >"$state_file"
}

read_state() {
	[ -f "$state_file" ] || die E_NO_SYNC_IN_PROGRESS "no materialized sync ($state_file missing); run --materialize <ref> first"
	st_head=$(sed -n 's/^head=//p' "$state_file")
	st_graft=$(sed -n 's/^graft=//p' "$state_file")
	st_upstream=$(sed -n 's/^upstream=//p' "$state_file")
	st_ref=$(sed -n 's/^ref=//p' "$state_file")
	st_prev=$(sed -n 's/^prev_graft=//p' "$state_file")
}

# ---------------------------------------------------------------- step 4

# replace_once FILE PERL-REGEX REPLACEMENT DESC: the pattern must match
# exactly once, so a pin that drifted out of shape fails loudly.
replace_once() {
	local file=$1 re=$2 rep=$3 desc=$4 n
	n=$(RE=$re perl -0777 -ne 'my $c = 0; $c++ while /$ENV{RE}/g; print $c' "$file")
	[ "$n" = 1 ] || die E_REPIN_PATTERN "$desc: expected exactly one match in $file, found $n"
	RE=$re REP=$rep perl -0777 -pi -e 's{$ENV{RE}}{my @g = map { defined $_ ? $_ : "" } ($1, $2, $3, $4, $5, $6, $7, $8, $9); (my $r = $ENV{REP}) =~ s/\$\{(\d)\}/$g[$1 - 1]/g; $r}e' "$file"
}

repin() {
	local up=$1 graft=$2 up_short old_short old_full old_version label changed
	up_short=$(short "$up")
	label=$(upstream_label "$up")
	old_short=$(sed -nE 's/^[[:space:]]*forkPointCommit = "([0-9a-f]+)"$/\1/p' tools/forkdiff-gen/main.go)
	[ -n "$old_short" ] || die E_REPIN_PATTERN "forkPointCommit not found in tools/forkdiff-gen/main.go"

	replace_once tools/forkdiff-gen/main.go '(\n\s*forkPointCommit = ")[0-9a-f]+(")' "\${1}$up_short\${2}" forkPointCommit
	replace_once live/forkdiff_test.go '(\nconst forkSurfaceForkPoint = ")[0-9a-f]+(")' "\${1}$up\${2}" forkSurfaceForkPoint
	replace_once live/ci_coverage_test.go '(\nconst upstreamBaseCommit = ")[0-9a-f]+(")' "\${1}$graft\${2}" upstreamBaseCommit
	replace_once README.md '(Built on OpenTofu )\S+( \(fork point\n\[`)[0-9a-f]+(`\]\(https://github\.com/opentofu/opentofu/commit/)[0-9a-f]+(\)\))' \
		"\${1}$label\${2}$up_short\${3}$up_short\${4}" "README base line"
	replace_once README.md '(\[opentofu/opentofu\]\(https://github\.com/opentofu/opentofu\) at `)[0-9a-f]+(`)' \
		"\${1}$up_short\${2}" "README license fork point"

	if [ "$old_short" != "$up_short" ] && ! grep -q "\"$old_short\":" live/upstream_base_label_test.go; then
		old_full=$(git rev-parse -q --verify "$old_short^{commit}" 2>/dev/null) || die E_UPSTREAM_MISSING "outgoing fork point $old_short is not in this object database; run: git fetch $UPSTREAM_REMOTE"
		old_version=$(git show "$old_full:version/VERSION" | tr -d '[:space:]')
		ENTRY="	// Until $(date -u +%Y-%m-%d): $old_version at $old_short. scripts/upstream-sync.sh moved the base to $up_short.
	\"$old_short\": \"$old_version\",
" perl -0777 -pi -e 's/(\nvar changelogPastBases = map\[string\]string\{\n.*?\n)(\}\n)/$1$ENV{ENTRY}$2/s or die "changelogPastBases not found\n"' live/upstream_base_label_test.go ||
			die E_REPIN_PATTERN "changelogPastBases map not found in live/upstream_base_label_test.go"
	fi

	changed=$(git status --porcelain -- tools/forkdiff-gen/main.go live/forkdiff_test.go live/ci_coverage_test.go live/upstream_base_label_test.go README.md)
	if [ -n "$changed" ] || ! grep -q "\"fork_point\": \"$up\"" live/fork-surface.json; then
		say "regenerating live/fork-surface.json"
		go run ./tools/forkdiff-gen || die E_REGENERATE "go run ./tools/forkdiff-gen failed"
		go run ./tools/forkdiff-gen -render || die E_REGENERATE "go run ./tools/forkdiff-gen -render failed"
	fi
	git add -- tools/forkdiff-gen/main.go live/forkdiff_test.go live/ci_coverage_test.go live/upstream_base_label_test.go README.md live/fork-surface.json live/fork-surface-summary.json
	[ ! -f live/FORK-SURFACE.md ] || git add -- live/FORK-SURFACE.md
	if git diff --cached --quiet; then
		say "pins already at $up_short (graft $(short "$graft")); nothing to re-pin"
	else
		git commit -q -m "upstream sync: re-pin fork point to $up_short (graft $(short "$graft"))" \
			-m "Re-pins forkPointCommit, forkSurfaceForkPoint, upstreamBaseCommit, changelogPastBases and the README base lines, and regenerates live/fork-surface.json. Written by scripts/upstream-sync.sh." ||
			die E_GIT "re-pin commit failed"
		say "re-pinned: $(git log -1 --format='%h %s')"
	fi
}

# ---------------------------------------------------------------- step 5

# list_diff prints how the experimental-runtime lane's expected-failures
# list (#2256 U3) differs from main's, for the sync PR's description. A sync
# PR re-measures the list with `make test-experimental-ratchet` and commits
# it; run `scripts/upstream-sync.sh --report` again afterwards to print the
# diff.
EXPECTED_FAILURES=live/experimental-runtime/expected-failures.json
list_diff() {
	local base
	base=$(git merge-base HEAD origin/main 2>/dev/null) || base=
	if [ -z "$base" ]; then
		say "expected-failures list: no merge base with origin/main to diff against"
		return
	fi
	say "expected-failures list ($EXPECTED_FAILURES) against main, for the sync PR's description:"
	if git diff --quiet "$base" -- "$EXPECTED_FAILURES"; then
		echo "  (unchanged)"
	else
		git diff "$base" -- "$EXPECTED_FAILURES" | sed 's/^/  /'
	fi
}

report() {
	list_diff
	say "upstream gap-table issues ($UPSTREAM_REPO, read-only):"
	local n line
	for n in $(gap_upstream_numbers); do
		if line=$(gh api "repos/$UPSTREAM_REPO/issues/$n" --jq '[(if .pull_request then "pr" else "issue" end), .state, (if .pull_request.merged_at then "merged " + .pull_request.merged_at[0:10] else "" end), "updated " + .updated_at[0:10], .title] | @tsv' 2>/dev/null); then
			printf '  %-6s %s\n' "$n" "$line"
		else
			printf '  %-6s (could not read; gh api failed)\n' "$n"
		fi
	done
}

# ---------------------------------------------------------------- modes

if [ "$mode" = report ]; then
	report
	exit 0
fi

if [ "$mode" = abort ]; then
	read_state
	[ "$(git rev-parse HEAD)" = "$st_head" ] || die E_HEAD_MOVED "HEAD moved since --materialize; resolve by hand"
	git read-tree --reset -u HEAD
	rm -f "$state_file"
	say "aborted; index and worktree reset to HEAD"
	exit 0
fi

if [ "$mode" = finish ]; then
	read_state
	require_branch_off_main
	[ "$(git rev-parse HEAD)" = "$st_head" ] || die E_HEAD_MOVED "HEAD is $(short HEAD), but the sync was materialized on $(short "$st_head")"
	if [ -n "$(git ls-files -u)" ]; then
		git ls-files -u | cut -f2 | sort -u | sed 's/^/  /' >&2
		die E_UNRESOLVED "unmerged paths remain (above); resolve and git add them"
	fi
	if [ -f "$state_dir/conflicts" ]; then
		markers=
		while IFS= read -r p; do
			[ -f "$p" ] || continue
			if grep -qE '^(<<<<<<<|>>>>>>>)( |$)' "$p"; then markers="$markers $p"; fi
		done <"$state_dir/conflicts"
		[ -z "$markers" ] || die E_MARKERS_LEFT "conflict markers remain in:$markers"
	fi
	git diff --quiet || die E_UNSTAGED "worktree differs from the index; git add your resolutions (or discard stray edits) first"
	tree=$(git write-tree)
	want_v=$(git show "$st_graft:version/VERSION")
	got_v=$(git show "$tree:version/VERSION" 2>/dev/null || true)
	[ "$want_v" = "$got_v" ] || die E_VERSION_EDITED "version/VERSION in the resolved tree is '$got_v', upstream's is '$want_v'; it must arrive through the merge unedited"
	msg=$tmp/merge-msg
	{
		echo "upstream sync: merge OpenTofu $(upstream_label "$st_upstream") ($(short "$st_upstream")) via graft $(short "$st_graft")"
		echo
		echo "Merges raw graft $(short "$st_graft") of upstream $(short "$st_upstream") (ref $st_ref)."
		echo "Merge base for the content merge: the rewritten twin of the previous"
		echo "graft $(short "$st_prev"). Conflicts resolved by hand after"
		echo "scripts/upstream-sync.sh --materialize; committed by --finish."
	} >"$msg"
	merge=$(git commit-tree "$tree" -p "$st_head" -p "$st_graft" -F "$msg") || die E_GIT "commit-tree for the merge failed"
	git update-ref -m "upstream-sync: merge $(short "$st_upstream")" HEAD "$merge" "$st_head"
	rm -f "$state_file" "$state_dir/conflicts"
	say "merged: $(git log -1 --format='%h %s')"
	repin "$st_upstream" "$st_graft"
	[ "$do_report" = 0 ] || report
	exit 0
fi

# sync, materialize, graft-only
check_upstream_remote
if [ "$do_fetch" = 1 ]; then
	git fetch --quiet "$UPSTREAM_REMOTE" || die E_GIT "git fetch $UPSTREAM_REMOTE failed"
	git fetch --quiet "$UPSTREAM_REMOTE" "refs/tags/$ref:refs/tags/$ref" 2>/dev/null || true
fi
up=$(resolve_upstream_ref "$ref")

if [ -n "$prev_graft_arg" ]; then
	if [ "$mode" = graft-only ]; then
		prev=$(validate_prev_graft "$prev_graft_arg" any)
	else
		prev=$(validate_prev_graft "$prev_graft_arg" latest)
	fi
else
	found=$(latest_graft)
	[ -n "$found" ] || die E_NO_PREV_GRAFT "no commit on HEAD's history has a subject starting 'graft: OpenTofu' or 'upstream: OpenTofu'; pass --prev-graft"
	prev=$(validate_prev_graft "$found" latest)
fi
prev_up=$(claim_commit "$prev")
say "upstream $ref = $(short "$up"); previous graft $(short "$prev") (upstream $(short "$prev_up"))"

if [ "$mode" = graft-only ]; then
	graft=$(make_graft "$up" "$prev")
	say "graft $(short "$graft") tree $(tree_of "$graft")"
	echo "$graft"
	exit 0
fi

if [ "$(tree_of "$prev")" = "$(tree_of "$up")" ]; then
	say "already merged: previous graft $(short "$prev") carries upstream $(short "$up")'s tree"
	[ -z "$(git status --porcelain -- tools/forkdiff-gen/main.go live README.md)" ] || die E_DIRTY_WORKTREE "pin files have uncommitted changes"
	if git symbolic-ref -q HEAD >/dev/null; then
		repin "$prev_up" "$prev"
	else
		say "detached HEAD: re-pin checked only"
		for p in "forkPointCommit = \"$(short "$up")\"" "forkSurfaceForkPoint = \"$up\"" "upstreamBaseCommit = \"$prev\""; do
			git grep -q -F "$p" HEAD -- tools/forkdiff-gen/main.go live/forkdiff_test.go live/ci_coverage_test.go ||
				die E_REPIN_PATTERN "pins are not at $(short "$up") ($p missing); re-run on a branch to re-pin"
		done
	fi
	[ "$do_report" = 0 ] || report
	exit 0
fi

graft=$(make_graft "$up" "$prev")
say "graft $(short "$graft") (tree $(short "$(tree_of "$graft")"), parent $(short "$prev"))"

say "building rewritten twins"
prev_twin=$(build_twin "$prev" prev)
new_twin=$(build_twin "$graft" new)

say "merge-tree --merge-base=twin($(short "$prev")) HEAD twin($(short "$graft"))"
set +e
git merge-tree --write-tree --merge-base="$prev_twin" HEAD "$new_twin" >"$tmp/merge.out"
rc=$?
set -e
[ "$rc" -le 1 ] || die E_GIT "git merge-tree failed (exit $rc)"

graft_branch="sync/$(short "$up")-graft"
git branch -f "$graft_branch" "$graft" >/dev/null
say "branch $graft_branch -> $(short "$graft")"

merged_tree=$(head -n1 "$tmp/merge.out")
# Conflicted file info: "mode oid stage<TAB>path" lines up to the first blank line.
awk 'NR > 1 && /^$/ { exit } NR > 1' "$tmp/merge.out" >"$tmp/stages"
awk 'f; /^$/ { f = 1 }' "$tmp/merge.out" >"$tmp/messages"

# Classify by merge-ort's messages. add/add is a content conflict in a file
# both sides created, so it counts as content.
perl -ne '
	if (/^CONFLICT \((content|add\/add)\): Merge conflict in (.+)$/) { print "content\t$2\n" }
	elsif (/^CONFLICT \(modify\/delete\): (.+?) deleted in .* and modified in /) { print "modify/delete\t$1\n" }
	elsif (/^CONFLICT \(rename\/delete\): (.+?) renamed to (.+?) in .*, but deleted in /) { print "rename/delete\t$1 -> $2\n" }
	elsif (/^CONFLICT \(([^)]+)\): (.*)$/) { print "other ($1)\t$2\n" }
' "$tmp/messages" | sort -u >"$tmp/conflicts"

if [ "$rc" = 1 ] || [ -s "$tmp/conflicts" ]; then
	n_content=$(grep -c '^content	' "$tmp/conflicts" || true)
	n_md=$(grep -c '^modify/delete	' "$tmp/conflicts" || true)
	n_rd=$(grep -c '^rename/delete	' "$tmp/conflicts" || true)
	n_other=$(grep -vcE '^(content|modify/delete|rename/delete)	' "$tmp/conflicts" || true)
	echo
	echo "CONFLICTS: $n_content content, $n_md modify/delete, $n_rd rename/delete, $n_other other"
	for k in content modify/delete rename/delete; do
		grep "^$k	" "$tmp/conflicts" | sed "s|^[^	]*	|  $k  |" || true
	done
	grep -vE '^(content|modify/delete|rename/delete)	' "$tmp/conflicts" | sed 's/	/  /; s/^/  /' || true
	echo
fi

if [ "$mode" = materialize ]; then
	require_branch_off_main
	require_clean_worktree
	[ ! -f "$state_file" ] || die E_SYNC_IN_PROGRESS "a materialized sync is already in progress ($state_file); --finish or --abort it"
	git read-tree --reset -u "$merged_tree" || die E_GIT "read-tree of the merge result failed"
	if [ -s "$tmp/stages" ]; then
		cut -f2 "$tmp/stages" | sort -u | awk '{ printf "0 0000000000000000000000000000000000000000\t%s\n", $0 }' >"$tmp/index-info"
		cat "$tmp/stages" >>"$tmp/index-info"
		git update-index --index-info <"$tmp/index-info" || die E_GIT "recording unmerged stages failed"
	fi
	write_state "$(git rev-parse HEAD)" "$graft" "$up" "$ref" "$prev" "$merged_tree"
	cut -f2 "$tmp/stages" | sort -u >"$state_dir/conflicts"
	say "materialized on $(git symbolic-ref --short HEAD): $(wc -l <"$state_dir/conflicts" | tr -d ' ') unmerged paths"
	say "resolve them, git add the results, then: scripts/upstream-sync.sh --finish"
	exit 0
fi

if [ "$rc" = 1 ]; then
	die E_CONFLICTS "merge of upstream $(short "$up") has conflicts (listed above); nothing was changed. To resolve: scripts/upstream-sync.sh --materialize --no-fetch $ref, then --finish"
fi

# Clean merge: commit it on the current branch, then re-pin.
require_branch_off_main
require_clean_worktree
head=$(git rev-parse HEAD)
want_v=$(git show "$graft:version/VERSION")
[ "$want_v" = "$(git show "$merged_tree:version/VERSION")" ] || die E_VERSION_EDITED "merged version/VERSION differs from upstream's '$want_v'"
git read-tree --reset -u "$merged_tree" || die E_GIT "read-tree of the merge result failed"
msg=$tmp/merge-msg
{
	echo "upstream sync: merge OpenTofu $(upstream_label "$up") ($(short "$up")) via graft $(short "$graft")"
	echo
	echo "Merges raw graft $(short "$graft") of upstream $(short "$up") (ref $ref)."
	echo "Merge base for the content merge: the rewritten twin of the previous"
	echo "graft $(short "$prev"). No conflicts. Written by scripts/upstream-sync.sh."
} >"$msg"
merge=$(git commit-tree "$merged_tree" -p "$head" -p "$graft" -F "$msg") || die E_GIT "commit-tree for the merge failed"
git update-ref -m "upstream-sync: merge $(short "$up")" HEAD "$merge" "$head"
say "merged: $(git log -1 --format='%h %s')"
repin "$up" "$graft"
[ "$do_report" = 0 ] || report
