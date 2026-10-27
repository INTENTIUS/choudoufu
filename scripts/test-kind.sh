#!/usr/bin/env bash
# `just test-kind`: the kind-gated pkg/staterecord tests, against a kind
# cluster of their own (GitHub issue #2288, ruling Q2). Maintainer-run at
# release; nothing in CI runs it.
#
# These tests need a real API server, because client-go's fake clientset
# assigns no resourceVersion and every version assertion in them would be
# vacuous against it. Until #2288 they ran as steps 1, 10 and 12 of the
# smoke scenario k8s-records-in-the-cluster; they are go_test proofs of
# claims 2 and 29 in live/smoke/claims.json now, and this is where they run.
#
# What it does:
#   1. creates a kind cluster with a scratch name, refusing if a cluster of
#      that name already exists (TEST_KIND_CLUSTER names it; the default is
#      unique to this run), at the node image pinned in live/kind-node-image;
#   2. creates the records namespace the tests write into (the store never
#      creates one);
#   3. runs `go test ./pkg/staterecord -run '^TestKubernetes' -v` with
#      CHOUDOUFU_K8S_TEST=1, CHOUDOUFU_K8S_RECORD_KUBECONFIG and
#      CHOUDOUFU_K8S_RECORD_NAMESPACE set, and keeps the log;
#   4. deletes the cluster it created, and only that one, on every exit.
#
# Read the verdict line at the end. A skipped test is not a pass: the log is
# what `go run ./tools/gotest-record -log <log>` records from, and it records
# only a test the log shows passing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for tool in kind kubectl go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "test-kind: $tool is not installed" >&2; exit 1; }
done

NAME="${TEST_KIND_CLUSTER:-chdf-test-kind-$(date +%Y%m%d%H%M%S)-$$}"
NS="tofu-records-test-kind"
IMAGE="${KIND_NODE_IMAGE:-$(cat "$ROOT/live/kind-node-image")}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/choudoufu-test-kind.XXXXXX")"
KC="$WORK/kubeconfig"
LOG="${TEST_KIND_LOG:-$WORK/test-kind.log}"

if kind get clusters 2>/dev/null | grep -qx "$NAME"; then
  echo "test-kind: a kind cluster named $NAME already exists. This recipe deletes the cluster it creates, so it will not run in one it did not create; name another with TEST_KIND_CLUSTER." >&2
  exit 1
fi

CREATED=0
teardown() {
  if [ "$CREATED" = "1" ]; then
    kind delete cluster --name "$NAME" >/dev/null 2>&1 \
      && echo "test-kind: deleted kind cluster $NAME" \
      || echo "test-kind: COULD NOT DELETE kind cluster $NAME - run: kind delete cluster --name $NAME" >&2
  fi
}
trap teardown EXIT

echo "test-kind: creating kind cluster $NAME ($IMAGE)"
# CREATED is set before the create returns, so a create that fails half way
# still has its cluster removed; the name was checked free above, so the
# cluster it removes is this run's.
CREATED=1
kind create cluster --image "$IMAGE" --name "$NAME" --kubeconfig "$KC" --wait 120s
kubectl --kubeconfig "$KC" create namespace "$NS" >/dev/null

echo "test-kind: go test ./pkg/staterecord -run '^TestKubernetes' -v   (log: $LOG)"
RC=0
( cd "$ROOT" && CHOUDOUFU_K8S_TEST=1 CHOUDOUFU_K8S_RECORD_KUBECONFIG="$KC" CHOUDOUFU_K8S_RECORD_NAMESPACE="$NS" \
    go test ./pkg/staterecord -run '^TestKubernetes' -count=1 -v ) > "$LOG" 2>&1 || RC=$?

PASSED="$(grep -cE '^--- PASS: TestKubernetes' "$LOG" || true)"
FAILED="$(grep -cE '^--- FAIL: TestKubernetes' "$LOG" || true)"
SKIPPED="$(grep -cE '^--- SKIP: TestKubernetes' "$LOG" || true)"
grep -E '^--- (PASS|FAIL|SKIP): TestKubernetes' "$LOG" || true
if [ "$RC" = "0" ] && [ "$FAILED" = "0" ] && [ "$SKIPPED" = "0" ] && [ "$PASSED" -gt 0 ]; then
  echo "TEST-KIND GREEN: $PASSED passed, 0 failed, 0 skipped. Record it: go run ./tools/gotest-record -log $LOG"
else
  echo "TEST-KIND RED: go test exited $RC; $PASSED passed, $FAILED failed, $SKIPPED skipped. Log: $LOG" >&2
  exit 1
fi
