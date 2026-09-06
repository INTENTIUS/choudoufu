#!/usr/bin/env python3
"""Idle-gap analysis of a plan, from a TF_LOG=DEBUG capture (issues #683, #867).

This is the instrument behind #683's head-of-line finding and #867's
re-measurement of it. #683's own copy lived on a sibling branch
(`live/wallclock-trace`) that no longer exists, which is the reason this one
is in the tree: the numbers in readconcurrency.go, sweepconcurrency.go and
site/content/docs/model/plan-cost.md are not reproducible without it.

What it measures, and the one definition everything else here rests on:

    in_flight(t) = (HTTP requests sent at or before t)
                 - (HTTP responses received at or before t)

Order does not matter for that count, which is why it is exact without
correlating individual requests to their own responses. An IDLE GAP is a span
of at least --min seconds during which in_flight is zero: the process is
holding no AWS request open at all. That is the quantity a peak-concurrency
statistic cannot show - #683 measured peak 10 on both sides of a comparison
where one side was idle 54% of its span and the other 30%.

Two things it cannot see, said rather than hidden:

  * Only the PROVIDER logs "HTTP Request Sent" / "HTTP Response Received".
    internal/live/cloudcontrol's client talks to Cloud Control and to the
    Tagging API over its own net/http client inside the tofu process and logs
    no line per HTTP request, so the sweep's own calls are invisible to the
    in-flight count. What the sweep does log is one line per type, and that is
    what the pass attribution below uses.
  * A gap is silence on the wire, not necessarily a stall in the fork: a
    process legitimately between phases is idle too. That is why every gap is
    printed with the log lines inside it, and why the "ends in `retrying
    request`" classification exists - a gap closed by an SDK retry is backoff,
    which is the shape #683 found.

Pass attribution is read off the provider's own `tf_rpc` attribute, not off
the clock. The sweep's per-type listing is a `tf_rpc=ListResource` call and
the read pass's import and read are `tf_rpc=ImportResourceState` and
`tf_rpc=ReadResource`, and the SDK stamps that attribute on the `retrying
request` line itself - so the stall is attributed to the pass whose OWN call
was in backoff, which is the question, rather than to whichever phase the
wall clock happened to be in. A gap with no retry line inside it falls back
to the `tf_rpc` of the request that closes it, and says so.

That matters here because the two passes overlap on the wire more than their
log lines suggest: on the 745-resource estate, listing one client-side-
filtered type fans out into hundreds of provider calls that are still arriving
while the read pass has started.

A log with no `stateless/discovery:` lines at all (stock terraform) reports
one undivided pass, named "graph walk", because stock has no sweep to
attribute anything to.

Usage:

    wallclock-gaps.py [--min SECONDS] LABEL=PATH [LABEL=PATH ...]
"""

import collections
import datetime
import re
import sys

TS = re.compile(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d+[-+]\d{4})\s")
RETRY = re.compile(r"retrying request ([^,]+), attempt (\d+)")
LEVEL = re.compile(r"\[(TRACE|DEBUG|INFO|WARN|ERROR)\]\s+(\S+)")
TFRPC = re.compile(r"tf_rpc=([A-Za-z]+)")

# Which pass each provider RPC belongs to. ListResource is the sweep's
# per-type listing (internal/live/discovery); ImportResourceState and
# ReadResource are the read pass's pair per instance
# (internal/live/projection). Anything else - ConfigureProvider,
# GetProviderSchema, ValidateResourceConfig - belongs to neither and is
# reported under its own name rather than folded into one of them.
PASS_OF_RPC = {
    "ListResource": "sweep",
    "ImportResourceState": "read",
    "ReadResource": "read",
}


def read(path):
    """Every timestamped line, sorted by its own timestamp."""
    lines = []
    with open(path, errors="replace") as fh:
        for line in fh:
            m = TS.match(line)
            if not m:
                continue
            t = datetime.datetime.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S.%f%z")
            lines.append((t, line.rstrip("\n")))
    lines.sort(key=lambda x: x[0])
    return lines


def report(label, path, minlen):
    lines = read(path)
    if not lines:
        print(f"{label}: no timestamped lines in {path}")
        return

    ev = [
        (t, +1 if "HTTP Request Sent" in l else -1)
        for t, l in lines
        if "HTTP Request Sent" in l or "HTTP Response Received" in l
    ]
    if not ev:
        print(f"{label}: no HTTP request/response lines in {path}")
        return

    t0 = ev[0][0]
    span = (ev[-1][0] - t0).total_seconds()
    sent = sum(1 for _, k in ev if k > 0)

    # The sweep is the only thing that logs stateless/discovery lines.
    sweep_end = None
    for t, l in lines:
        if "stateless/discovery:" in l:
            sweep_end = t
    sweep_s = (sweep_end - t0).total_seconds() if sweep_end else None

    cur = 0
    area = 0.0
    peak = 0
    zero = 0.0
    prev = t0
    gaps = []
    for t, k in ev:
        dt = (t - prev).total_seconds()
        area += cur * dt
        if cur == 0:
            zero += dt
            if dt >= minlen:
                gaps.append((prev, t, dt))
        cur += k
        peak = max(peak, cur)
        prev = t

    idle = sum(g[2] for g in gaps)
    largest = max((g[2] for g in gaps), default=0.0)

    print(f"\n===== {label} =====")
    print(f"  first request .. last response : {span:.1f}s   requests: {sent}")
    print(f"  peak in flight                 : {peak}")
    print(f"  time-weighted MEAN in flight   : {area / span:.2f}")
    print(f"  seconds with ZERO in flight    : {zero:.1f}s ({100 * zero / span:.0f}% of the span)")
    print(
        f"  idle gaps >= {minlen}s            : {len(gaps)}, totalling {idle:.1f}s "
        f"({100 * idle / span:.0f}% of the span)"
    )
    print(f"  largest single stall           : {largest:.2f}s")
    if sweep_s is None:
        print("  passes                         : one undivided pass (no sweep in this log)")
    else:
        print(
            f"  last stateless/discovery line  : t={sweep_s:.1f}s "
            f"(informational; stalls are attributed by tf_rpc, not by this)"
        )

    by_pass = collections.Counter()
    retry_closed = 0
    for a, b, dt in gaps:
        off = (a - t0).total_seconds()
        # `a < t <= b`, not `a < t < b`: hclog stamps at millisecond
        # resolution, and the SDK's `retrying request` line and the
        # `HTTP Request Sent` line that closes the gap routinely land in
        # the SAME millisecond (#683's own 10.74s stall does: both at
        # 19:09:55.954). An exclusive upper bound drops exactly the line
        # that says what the stall was, which is the one thing this
        # classification exists to read.
        inside = [l for t, l in lines if a < t <= b]
        retry_lines = [l for l in inside if RETRY.search(l)]
        closer = ""
        if retry_lines:
            retry_closed += 1
            last = RETRY.search(retry_lines[-1])
            closer = f"  ENDS IN RETRY: {last.group(1)} attempt {last.group(2)}"

        # The pass whose own call was in backoff, from tf_rpc on the retry
        # lines inside the gap; failing that, from the request that closes it.
        if sweep_s is None:
            which = "graph walk"
        else:
            rpcs = []
            for l in retry_lines:
                m = TFRPC.search(l)
                if m:
                    rpcs.append(PASS_OF_RPC.get(m.group(1), m.group(1)))
            if not rpcs:
                tail = [l for l in inside if "HTTP Request Sent" in l]
                for l in tail[-1:]:
                    m = TFRPC.search(l)
                    if m:
                        rpcs.append(PASS_OF_RPC.get(m.group(1), m.group(1)) + "?")
            names = sorted(set(rpcs))
            which = "+".join(names) if names else "unattributed"
        by_pass[which] += dt
        kinds = collections.Counter()
        for l in inside:
            m = LEVEL.search(l)
            kinds[f"{m.group(1)} {m.group(2)}" if m else "other"] += 1
        print(
            f"    gap t={off:6.1f}s for {dt:5.2f}s  [{which}]  "
            f"{len(inside)} log line(s) inside{closer}"
        )
        for k, n in kinds.most_common(3):
            print(f"        {n:5d}  {k}")

    print(f"  gaps ending in a `retrying request` line: {retry_closed} of {len(gaps)}")
    for which, secs in sorted(by_pass.items()):
        print(f"  idle attributed to the {which} pass: {secs:.1f}s")


def main(argv):
    minlen = 0.8
    args = []
    i = 0
    while i < len(argv):
        if argv[i] == "--min":
            minlen = float(argv[i + 1])
            i += 2
            continue
        args.append(argv[i])
        i += 1
    if not args:
        print(__doc__)
        return 2
    for arg in args:
        label, path = arg.split("=", 1)
        report(label, path, minlen)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
