// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"

	"github.com/intentius/choudoufu/internal/configs"
)

func TestBuildDefaultsWhenBlockIsAbsent(t *testing.T) {
	got := Build(nil)
	if got.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want the SDK default %d", got.MaxAttempts, DefaultMaxAttempts)
	}
	if got.Mode != DefaultMode {
		t.Errorf("Mode = %q, want %q", got.Mode, DefaultMode)
	}
	if problems := got.Validate(); len(problems) != 0 {
		t.Errorf("the defaults must validate, got %v", problems)
	}
}

// TestBuildTakesOnlyWhatWasWritten pins the "absent means absent" contract the
// policy, record_store and strict blocks all share: an argument left out
// resolves to the default even when the other argument was written.
func TestBuildTakesOnlyWhatWasWritten(t *testing.T) {
	got := Build(&configs.LiveRetry{MaxAttempts: 9, MaxAttemptsSet: true})
	if got.MaxAttempts != 9 {
		t.Errorf("MaxAttempts = %d, want 9", got.MaxAttempts)
	}
	if got.Mode != DefaultMode {
		t.Errorf("Mode = %q, want the default %q when mode was not written", got.Mode, DefaultMode)
	}

	got = Build(&configs.LiveRetry{Mode: string(ModeAdaptive), ModeSet: true})
	if got.MaxAttempts != DefaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want the default %d when max_attempts was not written", got.MaxAttempts, DefaultMaxAttempts)
	}
	if got.Mode != ModeAdaptive {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeAdaptive)
	}
}

// TestBuildDoesNotJudge is the layering pin. internal/configs records what was
// written, this package resolves it, internal/live/lint refuses it - so an
// invalid setting must survive Build intact rather than being silently
// corrected here, or lint would have nothing left to point at.
func TestBuildDoesNotJudge(t *testing.T) {
	got := Build(&configs.LiveRetry{
		MaxAttempts: 0, MaxAttemptsSet: true,
		Mode: "aggressive", ModeSet: true,
	})
	if got.MaxAttempts != 0 {
		t.Errorf("MaxAttempts = %d, want the written 0 to survive Build", got.MaxAttempts)
	}
	if string(got.Mode) != "aggressive" {
		t.Errorf("Mode = %q, want the written value to survive Build", got.Mode)
	}
	if len(got.Validate()) != 2 {
		t.Errorf("Validate should complain about both arguments, got %v", got.Validate())
	}
}

func TestValidateBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"the SDK default", Config{MaxAttempts: DefaultMaxAttempts, Mode: ModeStandard}, false},
		{"one attempt is the floor", Config{MaxAttempts: 1, Mode: ModeStandard}, false},
		{"zero attempts never calls the cloud", Config{MaxAttempts: 0, Mode: ModeStandard}, true},
		{"negative attempts", Config{MaxAttempts: -1, Mode: ModeStandard}, true},
		{"the ceiling itself is allowed", Config{MaxAttempts: MaxMaxAttempts, Mode: ModeStandard}, false},
		{"past the ceiling", Config{MaxAttempts: MaxMaxAttempts + 1, Mode: ModeStandard}, true},
		{"ten, the scale-128 setting", Config{MaxAttempts: 10, Mode: ModeAdaptive}, false},
		{"an invented mode", Config{MaxAttempts: 3, Mode: "aggressive"}, true},
		{"an empty mode", Config{MaxAttempts: 3, Mode: ""}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := tc.cfg.Validate()
			if got := len(problems) > 0; got != tc.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", problems, tc.wantErr)
			}
		})
	}
}

// TestRetryValidateSentencesArePointable is what internal/live/lint's
// isAboutMode relies on: a mode complaint must start with "mode" and an
// attempt-count complaint must not, so each sentence can be pointed at the
// argument that caused it. If a future rule breaks this, the diagnostic would
// silently point at the wrong argument, which is worse than no diagnostic.
func TestRetryValidateSentencesArePointable(t *testing.T) {
	modeProblems := Config{MaxAttempts: 3, Mode: "aggressive"}.Validate()
	if len(modeProblems) != 1 {
		t.Fatalf("want exactly one complaint about mode, got %v", modeProblems)
	}
	if !strings.HasPrefix(modeProblems[0], "mode") {
		t.Errorf("a mode complaint must start with %q so lint can point at the right argument, got %q", "mode", modeProblems[0])
	}

	attemptProblems := Config{MaxAttempts: 0, Mode: ModeStandard}.Validate()
	if len(attemptProblems) != 1 {
		t.Fatalf("want exactly one complaint about max_attempts, got %v", attemptProblems)
	}
	if strings.HasPrefix(attemptProblems[0], "mode") {
		t.Errorf("an attempt-count complaint must not start with %q, got %q", "mode", attemptProblems[0])
	}
}

// throttlingAPIError is the shape a service returns once the retryer has given
// up: a smithy API error carrying a throttling code.
type throttlingAPIError struct{ code string }

func (e throttlingAPIError) Error() string                 { return e.code + ": Rate exceeded" }
func (e throttlingAPIError) ErrorCode() string             { return e.code }
func (e throttlingAPIError) ErrorMessage() string          { return "Rate exceeded" }
func (e throttlingAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestIsThrottling(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"SSM's throttling code", throttlingAPIError{"ThrottlingException"}, true},
		{"EC2's spelling", throttlingAPIError{"RequestLimitExceeded"}, true},
		{"S3's spelling", throttlingAPIError{"SlowDown"}, true},
		{"a wrapped throttle", errors.New("outer: " + throttlingAPIError{"Throttling"}.Error()), false},
		{"a genuine refusal", throttlingAPIError{"AccessDeniedException"}, false},
		{"an ordinary error", errors.New("connection reset"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsThrottling(tc.err); got != tc.want {
				t.Errorf("IsThrottling(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestThrottleAdviceNamesTheMovesNotJustTheAttemptCount is issue #1148's
// actual complaint: a reader should not have to know a service's quota model
// to understand why a write failed. It used to pin Parameter Store's numbers
// too; that backend is retired (GitHub issue #1346) and no other backend has
// measured numbers to pin.
func TestThrottleAdviceNamesTheMovesNotJustTheAttemptCount(t *testing.T) {
	advice := ThrottleAdvice(throttlingAPIError{"SlowDown"}, "s3", Config{MaxAttempts: 3, Mode: ModeStandard})
	for _, want := range []string{
		"3 attempt(s)",
		"adaptive",
		"max_attempts",
	} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice should name %q, got:\n%s", want, advice)
		}
	}

	// Not throttling: no advice, rather than advice that misleads.
	if got := ThrottleAdvice(throttlingAPIError{"AccessDeniedException"}, "s3", Config{MaxAttempts: 3}); got != "" {
		t.Errorf("a non-throttling error must get no throttling advice, got %q", got)
	}

	// No backend is told about a retired one's ceiling.
	for _, never := range []string{"Parameter Store", "40 transactions", "high-throughput"} {
		if strings.Contains(advice, never) {
			t.Errorf("the advice still mentions %q:\n%s", never, advice)
		}
	}
}

// countingThrottler is an http.Client stand-in that answers every request with
// a throttling response and counts how many it was asked for.
type countingThrottler struct{ n atomic.Int32 }

func (c *countingThrottler) Do(*http.Request) (*http.Response, error) {
	c.n.Add(1)
	body := `{"__type":"ThrottlingException","message":"Rate exceeded"}`
	return &http.Response{
		StatusCode: 400,
		Header:     http.Header{"Content-Type": []string{"application/x-amz-json-1.1"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

// TestMaxAttemptsReachesTheClient is the red arm this block was specified
// with, and the only test here that proves the setting does anything.
//
// Every other test in this file would pass if Options returned nothing at all:
// they check that a number is resolved, a sentence is worded, a bound is
// enforced. None of them touches a client. So this one builds a real SSM
// client through the same path the record store uses, answers every request
// with a throttling response, and counts the requests that actually left.
//
// The pairing is the proof. max_attempts = 1 must produce exactly one request
// - a budget that is not wired up would retry the SDK's default three times
// and this would catch it - and max_attempts = 5 must produce exactly five,
// which a hard-coded 1 would fail. A single-value assertion could pass against
// a client that ignores the configuration entirely.
func TestMaxAttemptsReachesTheClient(t *testing.T) {
	for _, attempts := range []int{1, 5} {
		t.Run(fmt.Sprintf("max_attempts=%d", attempts), func(t *testing.T) {
			stub := &countingThrottler{}
			cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
				append(
					Config{MaxAttempts: attempts, Mode: ModeStandard}.Options(),
					awsconfig.WithRegion("us-east-2"),
					awsconfig.WithHTTPClient(stub),
					awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
					// Keep the test off the machine's own AWS environment:
					// a developer with AWS_MAX_ATTEMPTS exported would
					// otherwise be testing their shell, not this package.
					awsconfig.WithSharedConfigFiles(nil),
					awsconfig.WithSharedCredentialsFiles(nil),
				)...,
			)
			if err != nil {
				t.Fatalf("LoadDefaultConfig: %s", err)
			}

			_, err = ssm.NewFromConfig(cfg).PutParameter(t.Context(), &ssm.PutParameterInput{Name: aws.String("/t/n"), Value: aws.String("v")})
			if err == nil {
				t.Fatal("a client answered only with throttling responses must fail")
			}
			if !IsThrottling(err) {
				t.Errorf("the terminal error should still read as throttling, got %v", err)
			}
			if got := int(stub.n.Load()); got != attempts {
				t.Errorf("the client made %d request(s), want exactly max_attempts = %d", got, attempts)
			}
		})
	}
}

// TestOptionsOverrideTheEnvironment pins the direction chosen in
// [Config.Options]: a configured value must beat AWS_MAX_ATTEMPTS in the
// environment, so a run's retry behaviour is the one its configuration
// records. The scale-128 certification passed because of two environment
// variables nobody reading its evidence could see; that is the situation this
// prevents from recurring silently.
func TestOptionsOverrideTheEnvironment(t *testing.T) {
	t.Setenv("AWS_MAX_ATTEMPTS", "2")
	t.Setenv("AWS_RETRY_MODE", "standard")

	stub := &countingThrottler{}
	cfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		append(
			Config{MaxAttempts: 4, Mode: ModeStandard}.Options(),
			awsconfig.WithRegion("us-east-2"),
			awsconfig.WithHTTPClient(stub),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
			awsconfig.WithSharedConfigFiles(nil),
			awsconfig.WithSharedCredentialsFiles(nil),
		)...,
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %s", err)
	}
	if _, err := ssm.NewFromConfig(cfg).PutParameter(t.Context(), &ssm.PutParameterInput{Name: aws.String("/t/n"), Value: aws.String("v")}); err == nil {
		t.Fatal("want a throttling failure")
	}
	if got := int(stub.n.Load()); got != 4 {
		t.Errorf("the client made %d request(s), want the configured 4 rather than the environment's 2", got)
	}
}
