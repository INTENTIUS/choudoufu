#!/usr/bin/env bash
# Checks whether a background subagent (dispatched via the Agent tool) is
# still actively producing output, without pulling its full transcript into
# the caller's context - only mtime/size/line-count, the last few raw shell
# commands it ran, and any assistant "text" it has narrated.
#
# This exists because checking on N running agents by hand - readlink -f the
# .output symlink, stat its mtime, tail -c NNNN | grep -o '"command":...' -
# is exactly the kind of thing that gets re-derived from scratch each time,
# burning tokens pulling raw JSONL into context to answer "is it stuck".
# Making it a script means the check is identical every time, for an
# orchestrating agent or for the maintainer's own use, and it costs a few
# hundred bytes of output instead of a multi-thousand-line transcript dump.
#
# Usage:
#   .claude/scripts/agent-progress.sh <task-id> [task-id...]
#   STALE_AFTER_SECONDS=600 .claude/scripts/agent-progress.sh <task-id>

set -euo pipefail

STALE_AFTER_SECONDS="${STALE_AFTER_SECONDS:-300}"

if [ "$#" -eq 0 ]; then
    echo "usage: $0 <task-id> [task-id...]" >&2
    exit 2
fi

now_epoch=$(date +%s)

for id in "$@"; do
    echo "=== $id ==="
    out_link=$(find /private/tmp/claude-501 -maxdepth 4 -path "*/tasks/${id}.output" 2>/dev/null | head -1)
    if [ -z "$out_link" ]; then
        echo "no .output file found under /private/tmp/claude-501 for this id"
        echo
        continue
    fi

    target=$(readlink -f "$out_link" 2>/dev/null || greadlink -f "$out_link" 2>/dev/null || echo "$out_link")
    if [ ! -f "$target" ]; then
        echo "symlink target missing: $out_link -> $target"
        echo
        continue
    fi

    mtime_epoch=$(stat -f "%m" "$target" 2>/dev/null || stat -c "%Y" "$target")
    size=$(stat -f "%z" "$target" 2>/dev/null || stat -c "%s" "$target")
    lines=$(wc -l < "$target" | tr -d ' ')
    age=$((now_epoch - mtime_epoch))

    echo "transcript: $target"
    echo "size=${size}B lines=${lines} last-write=${age}s ago"
    if [ "$age" -gt "$STALE_AFTER_SECONDS" ]; then
        echo "verdict: POSSIBLY STALLED (no write in ${age}s, threshold ${STALE_AFTER_SECONDS}s)"
    else
        echo "verdict: ACTIVE"
    fi

    echo "--- last shell command(s) ---"
    tail -c 8000 "$target" | grep -o '"command":"[^"]*"' | tail -3 || echo "(none found in tail window)"

    echo "--- last assistant text (e.g. a final summary, if the agent has finished narrating) ---"
    tail -c 20000 "$target" | grep -o '"text":"[^"]*"' | tail -3 || echo "(none found in tail window)"

    echo
done
