// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package retry is the vocabulary behind a live block's "retry" block: which
// mode spellings mean anything, what bounds an attempt count has, and how the
// resolved settings reach the aws-sdk-go-v2 clients every live command builds.
//
// It is the counterpart to internal/live/strict and internal/live/policy.
// internal/configs records what an author wrote and judges none of it; this
// package says what the spellings mean; internal/live/lint refuses the ones
// that mean nothing. The layering exists so that internal/configs stays free
// of semantics, and it is why a mode of "aggressive" decodes cleanly and is
// refused a layer later by name.
//
// # Why this is configurable at all
//
// aws-sdk-go-v2 defaults to three attempts. Three is enough for an estate of
// tens of resources and not enough for one that writes thousands of records:
// a scale-50 certification against real AWS failed test_apply on nothing but
//
//	exceeded maximum number of attempts, 3 ... ThrottlingException: Rate exceeded
//
// while the plan it was applying was empty. The scale-128 run that followed
// cleared only because AWS_RETRY_MODE=adaptive and AWS_MAX_ATTEMPTS=10 were
// exported by hand from outside the tool - the right fix in the wrong place,
// invisible in the configuration and absent from the recorded evidence.
// Issues #1196 and #1148.
package retry

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"

	"github.com/intentius/choudoufu/internal/configs"
)

// Mode is a spelling of the "mode" argument. The two values are the SDK's own
// retry modes, named here rather than re-derived so that a diagnostic can list
// them without importing the SDK into internal/live/lint.
type Mode string

const (
	// ModeStandard is aws-sdk-go-v2's default: a fixed attempt budget with a
	// client-side rate limiter shared across the client's requests.
	ModeStandard Mode = "standard"

	// ModeAdaptive adds a send-rate limiter that slows down in response to
	// throttling and speeds back up when it stops. It is the mode that turned
	// a failing scale-50 run into a passing scale-128 one, and the mode an
	// estate writing thousands of records wants.
	ModeAdaptive Mode = "adaptive"
)

// DefaultMaxAttempts is what an estate gets when the retry block sets no
// max_attempts, and what it got before the block existed: aws-sdk-go-v2's own
// default, read from the SDK rather than copied, so a change upstream cannot
// leave this package quietly disagreeing with the client it configures.
const DefaultMaxAttempts = awsretry.DefaultMaxAttempts

// DefaultMode is what an estate gets when the retry block sets no mode.
const DefaultMode = ModeStandard

// MaxMaxAttempts bounds max_attempts.
//
// The ceiling is not a guess about clouds; it is a guard against a
// configuration that turns a failing run into a hanging one. Every attempt
// past the first costs its own backoff, so a large budget spends wall-clock
// rather than succeeding, and a run that cannot make progress should fail
// loudly instead of retrying for an hour. Thirty is far above anything a real
// estate has needed - the scale-128 certification cleared on ten - and far
// below the point where a wedged call looks like a hang.
const MaxMaxAttempts = 30

// ValidModes returns the mode spellings this fork accepts, sorted, for a
// diagnostic that has to list them.
func ValidModes() []string {
	out := []string{string(ModeStandard), string(ModeAdaptive)}
	sort.Strings(out)
	return out
}

// ValidMode reports whether s is a mode spelling this fork accepts.
func ValidMode(s string) bool {
	switch Mode(s) {
	case ModeStandard, ModeAdaptive:
		return true
	default:
		return false
	}
}

// Config is the resolved retry settings for one estate: every field filled in,
// with the defaults already applied, so no reader has to know which arguments
// were written out. Build produces it; a nil *configs.LiveRetry produces the
// defaults, which is what a configuration written before this block existed
// gets.
type Config struct {
	// MaxAttempts is the total number of tries a cloud call gets, the first
	// one included - the way aws-sdk-go-v2 counts.
	MaxAttempts int

	// Mode is the resolved retry mode.
	Mode Mode
}

// Build resolves a decoded retry block into the settings the clients use.
//
// It applies defaults and nothing else. An out-of-range attempt count or an
// invented mode reaches this function intact and is resolved as written,
// because refusing it is internal/live/lint's job and doing it twice would
// mean two places that must agree about the vocabulary. Callers that build a
// client from a configuration lint has not seen should call [Config.Validate]
// first.
func Build(rt *configs.LiveRetry) Config {
	cfg := Config{MaxAttempts: DefaultMaxAttempts, Mode: DefaultMode}
	if rt == nil {
		return cfg
	}
	if rt.MaxAttemptsSet {
		cfg.MaxAttempts = rt.MaxAttempts
	}
	if rt.ModeSet {
		cfg.Mode = Mode(rt.Mode)
	}
	return cfg
}

// Validate reports what is wrong with a resolved Config, as a list of
// human-readable sentences, or nil when it is usable. It is the single
// statement of the rules; internal/live/lint turns each sentence into a
// diagnostic pointed at the argument that caused it.
func (c Config) Validate() []string {
	var problems []string
	if c.MaxAttempts < 1 {
		problems = append(problems, fmt.Sprintf(
			"max_attempts must be at least 1, because the first try is an attempt; %d would mean never calling the cloud at all", c.MaxAttempts))
	}
	if c.MaxAttempts > MaxMaxAttempts {
		problems = append(problems, fmt.Sprintf(
			"max_attempts must be at most %d; %d spends wall-clock on backoff rather than succeeding, and a call that cannot make progress should fail loudly instead of retrying for an hour",
			MaxMaxAttempts, c.MaxAttempts))
	}
	if !ValidMode(string(c.Mode)) {
		problems = append(problems, fmt.Sprintf(
			"mode %q is not a retry mode this fork knows; the modes are %s",
			c.Mode, strings.Join(ValidModes(), " and ")))
	}
	return problems
}

// Options returns the aws-sdk-go-v2 load options that put this Config into
// effect, to be appended to whatever a caller already passes to
// config.LoadDefaultConfig.
//
// Both options are returned unconditionally, including when the values equal
// the SDK's own defaults. That is deliberate: passing them explicitly means
// the estate's configuration wins over AWS_MAX_ATTEMPTS and AWS_RETRY_MODE in
// the environment, so a run's retry behaviour is the one its configuration
// records rather than whatever the surrounding shell happened to export. The
// scale-128 certification is exactly the case - it passed because of two
// environment variables nobody reading the evidence could see.
func (c Config) Options() []func(*awsconfig.LoadOptions) error {
	mode := aws.RetryModeStandard
	if c.Mode == ModeAdaptive {
		mode = aws.RetryModeAdaptive
	}
	return []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRetryMaxAttempts(c.MaxAttempts),
		awsconfig.WithRetryMode(mode),
	}
}

// IsThrottling reports whether err is, anywhere in its chain, a cloud
// throttling response rather than a refusal of the request itself.
//
// It asks the SDK's own classifier rather than matching strings, so a code
// this fork has never heard of still counts, and then falls back to the
// smithy API error codes for the throttling family - which is what actually
// reaches a caller once the retryer has given up and wrapped the last
// attempt's error.
func IsThrottling(err error) bool {
	if err == nil {
		return false
	}
	if awsretry.IsErrorThrottles(awsretry.DefaultThrottles).IsErrorThrottle(err).Bool() {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "ThrottlingException", "Throttling", "ThrottledException",
			"RequestThrottled", "RequestThrottledException",
			"RequestLimitExceeded", "TooManyRequestsException",
			"ProvisionedThroughputExceededException", "SlowDown",
			"EC2ThrottledException", "TransactionInProgressException":
			return true
		}
	}
	return false
}

// ThrottleAdvice returns the sentence to add to a terminal throttling failure,
// or "" when err is not throttling.
//
// Issue #1148's complaint, in its own words: when a record write finally fails
// on throttling, the error names an attempt count, and a reader has to already
// know Parameter Store's quota model to understand it. An attempt count says
// how many times we asked; it does not say what said no, what the ceiling is,
// or which of the two available moves - raise the ceiling, or spend more
// attempts under it - applies. This supplies all three, in the reader's terms.
//
// store names the record store backend ("ssm", "s3", "local") so the advice
// can be specific about which service's ceiling was hit; an unrecognised
// backend gets the general form rather than a wrong quota number.
func ThrottleAdvice(err error, store string, c Config) string {
	if !IsThrottling(err) {
		return ""
	}
	var b strings.Builder
	// A zero Config means the caller could not reach the estate's retry
	// settings from where it stands. Say nothing about attempts rather than
	// reporting "0 attempt(s)", which would be false and would send a reader
	// looking for a setting that is not the problem.
	if c.MaxAttempts > 0 {
		fmt.Fprintf(&b, "The cloud throttled this call and it did not succeed within %d attempt(s) in %q mode.",
			c.MaxAttempts, c.Mode)
	} else {
		b.WriteString("The cloud throttled this call and it did not succeed within the attempts it was given.")
	}
	if store == "ssm" {
		b.WriteString(
			" The record store is SSM Parameter Store, whose default throughput is 40 transactions per second," +
				" shared across the account and region - an estate that writes a record per resource reaches that on" +
				" its own. The account setting /ssm/parameter-store/high-throughput-enabled raises it, and is the" +
				" durable fix; it is charged per API interaction above the standard rate.")
	}
	if c.MaxAttempts > 0 && c.Mode != ModeAdaptive {
		fmt.Fprintf(&b,
			" This run used %q mode, which spends its attempts without slowing down in response to throttling;"+
				" `mode = \"adaptive\"` in the live block's retry block backs off against the service's own signal.",
			c.Mode)
	}
	if c.MaxAttempts > 0 && c.MaxAttempts < 10 {
		fmt.Fprintf(&b,
			" Raising max_attempts above %d in that same block buys more tries under whatever ceiling is in force.",
			c.MaxAttempts)
	}
	return b.String()
}
