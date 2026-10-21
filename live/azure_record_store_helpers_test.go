// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The azure lane's request counter in live/e2e/lib/gauntlet.sh (#1120).
// The record-store helpers beside it (gauntlet_floci_az_record_account,
// gauntlet_floci_az_blobs) are driven by tools/gauntlet's
// azurerecords_test.go, and gauntlet_floci_az_record_store's block by this
// package's azure_endpoints_test.go.

// TestGauntletFlociAzRequestsBetweenCountsOneSpan reads a stubbed container
// log: the requests between the two marks are counted, the marks and
// everything outside them are not, and a span whose end mark is missing (an
// access log that is off writes no marks at all) is a failure, never 0.
func TestGauntletFlociAzRequestsBetweenCountsOneSpan(t *testing.T) {
	log := strings.Join([]string{
		`127.0.0.1 - - [21/Oct/2026:10:00:00 +0000] "GET /subscriptions/s/resourceGroups/rg?api-version=1 HTTP/1.1" 200 10`,
		`127.0.0.1 - - [21/Oct/2026:10:00:01 +0000] "GET /ready?gauntlet-mark=p-start HTTP/1.1" 200 2`,
		`127.0.0.1 - - [21/Oct/2026:10:00:02 +0000] "POST /tenant/oauth2/v2.0/token HTTP/1.1" 200 900`,
		`2026-10-21 10:00:02,100 INFO  [i.f.a.s.a.ArmHandler] some log line that is not a request`,
		`127.0.0.1 - - [21/Oct/2026:10:00:03 +0000] "POST /providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01 HTTP/1.1" 200 50`,
		`127.0.0.1 - - [21/Oct/2026:10:00:04 +0000] "GET /choudoufuazrarecords/green?restype=container&comp=list HTTP/1.1" 200 400`,
		`127.0.0.1 - - [21/Oct/2026:10:00:05 +0000] "GET /ready?gauntlet-mark=p-end HTTP/1.1" 200 2`,
		`127.0.0.1 - - [21/Oct/2026:10:00:06 +0000] "PUT /after HTTP/1.1" 200 2`,
	}, "\n")
	logf := filepath.Join(t.TempDir(), "docker.log")
	if err := os.WriteFile(logf, []byte(log+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runBash(t, `source e2e/lib/gauntlet.sh
docker() { [ "$1" = logs ] && cat "$LOGF"; }
printf 'span=%s\n' "$(gauntlet_floci_az_requests_between c p-start p-end)"
if gauntlet_floci_az_requests_between c p-start missing-end >/dev/null; then echo open=ok; else echo open=refused; fi`, "LOGF="+logf)
	if !strings.Contains(out, "span=3\n") {
		t.Errorf("want the three requests between the marks counted:\n%s", out)
	}
	if !strings.Contains(out, "open=refused\n") {
		t.Errorf("a span with no end mark was counted rather than refused:\n%s", out)
	}
}
