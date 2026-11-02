package cloudcontrol

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// captureLog redirects the standard logger for the duration of fn and returns
// what was written.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	fn()
	return buf.String()
}

// throttleThenOK serves n throttle responses and then a success.
func throttleThenOK(t *testing.T, n int) *httptest.Server {
	t.Helper()
	var seen int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen < n {
			seen++
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"__type":"ThrottlingException","message":"Rate exceeded"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
}

func testTaggingClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c := NewTagging(Config{
		Region:   "us-east-2",
		Endpoint: endpoint,
		// Keep the test fast: the backoff schedule is not what is under test.
		RetrySleep: func(ctx context.Context, d time.Duration) error { return nil },
	})
	return c
}

// TestRetryLineMatchesTheAnalyzerFormat is the load-bearing guard. live-cert's
// analyze_debug_log and the wall-clock gap analysis both key on the AWS
// provider's exact phrasing, "retrying request <Service>/<Operation>, attempt
// N". Before this logging existed, call() slept on a throttle silently, so
// every throttle choudoufu's own clients absorbed was counted as zero while
// the provider's were fully accounted - which is how a plan could stall on
// backoff that no instrument could see. If the wording drifts, these retries
// go back to being invisible and nothing else fails.
func TestRetryLineMatchesTheAnalyzerFormat(t *testing.T) {
	srv := throttleThenOK(t, 2)
	defer srv.Close()
	c := testTaggingClient(t, srv.URL)

	out := captureLog(t, func() {
		if _, err := c.GetResources(context.Background(), nil, nil); err != nil {
			t.Fatalf("GetResources after retries: %v", err)
		}
	})

	// The pattern live-cert greps for.
	re := regexp.MustCompile(`retrying request ([^,]+), attempt (\d+)`)
	got := re.FindAllStringSubmatch(out, -1)
	if len(got) != 2 {
		t.Fatalf("want 2 retry lines matching the analyzer pattern, got %d\nlog:\n%s", len(got), out)
	}
	for i, m := range got {
		if wantAttempt := []string{"2", "3"}[i]; m[2] != wantAttempt {
			t.Errorf("retry %d: attempt = %q, want %q (the SDK calls the first retry attempt 2)", i, m[2], wantAttempt)
		}
		if m[1] != "Resource Groups Tagging/GetResources" {
			t.Errorf("retry %d: action = %q, want %q", i, m[1], "Resource Groups Tagging/GetResources")
		}
	}

	// analyze_debug_log's throttle grep must also match, or the throttle and
	// retry counts fall out of step with each other.
	throttleRe := regexp.MustCompile(`ThrottlingException|Throttling:|TooManyRequestsException|Rate exceeded|RequestLimitExceeded`)
	if n := len(throttleRe.FindAllString(out, -1)); n < 2 {
		t.Errorf("throttle-shaped lines = %d, want >= 2; the harness pairs throttle lines with retry lines", n)
	}
}

// TestEveryRequestIsLogged proves the tag sweep is visible at all. The whole
// blind spot was that discovery logged one summary line per TYPE while the
// GetResources calls themselves left no trace, so a TF_LOG capture contained
// the provider's requests and none of choudoufu's own.
func TestEveryRequestIsLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testTaggingClient(t, srv.URL)

	out := captureLog(t, func() {
		for i := 0; i < 3; i++ {
			if _, err := c.GetResources(context.Background(), nil, nil); err != nil {
				t.Fatalf("GetResources: %v", err)
			}
		}
	})

	if n := strings.Count(out, "HTTP Request Sent"); n != 3 {
		t.Errorf("HTTP Request Sent lines = %d, want 3\nlog:\n%s", n, out)
	}
	if n := strings.Count(out, "HTTP Response Received"); n != 3 {
		t.Errorf("HTTP Response Received lines = %d, want 3\nlog:\n%s", n, out)
	}
	if !strings.Contains(out, "rpc.method=GetResources") {
		t.Errorf("no rpc.method=GetResources in the log; an analyzer cannot attribute the call\nlog:\n%s", out)
	}
	if !strings.Contains(out, "rpc.service=Resource Groups Tagging") {
		t.Errorf("no rpc.service for the Tagging API; the sweep would be unattributable\nlog:\n%s", out)
	}
}
