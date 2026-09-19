// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/smithy-go"
)

// KMSDeniedError is an S3 request refused because of the bucket's KMS key,
// and not because of anything about S3.
//
// A bucket whose default encryption is a customer managed key makes every
// GetObject a kms:Decrypt and every PutObject a kms:GenerateDataKey, made by
// S3 with the caller's identity. When KMS refuses, S3 reports it as a plain
// 403 AccessDenied on the S3 operation, and the only sign that the cause is
// the key is inside the message text. An operator who reads "AccessDenied ...
// PutObject" goes to the bucket policy and the role's S3 statements, which
// are correct, and the mistake is almost always somewhere they did not look:
// a key policy that does not name the role. A customer managed key is usable
// only by the principals its key policy allows, and an IAM policy alone never
// grants it unless the key policy delegates to IAM.
//
// The wording below was written against what real AWS says (GitHub issue
// #1345, us-east-2, 2026-09-18), which is, on one line:
//
//	User: arn:aws:sts::<acct>:assumed-role/<role>/<session> is not authorized
//	to perform: kms:GenerateDataKey on resource: arn:aws:kms:<region>:<acct>:key/<id>
//	because no resource-based policy allows the kms:GenerateDataKey action
type KMSDeniedError struct {
	// Action is the KMS action refused, for example "kms:Decrypt".
	Action string
	// KeyARN is the key that refused it. Empty if AWS did not say.
	KeyARN string
	// Principal is who was refused, as AWS names them. Empty if AWS did not say.
	Principal string
	// Where is which policy AWS blamed: KMSDeniedByKeyPolicy,
	// KMSDeniedByIdentityPolicy, KMSDeniedExplicitly, or "" when the message
	// did not say.
	Where string
	// Err is the S3 error as it arrived.
	Err error
}

// What AWS blames a KMS denial on.
const (
	KMSDeniedByKeyPolicy      = "key-policy"
	KMSDeniedByIdentityPolicy = "identity-policy"
	KMSDeniedExplicitly       = "explicit-deny"
)

var (
	kmsDeniedAction = regexp.MustCompile(`not authorized to perform: (kms:[A-Za-z*]+)`)
	// The quotes are optional and are not part of the ARN. AWS quotes the
	// resource in some of these messages and not others - the S3 denial in
	// kmsdenied_test.go's own fixture is quoted, the KMS one measured on
	// #1345 is not - and an unquoted pattern swallowed the closing quote into
	// the ARN, which made KeyARN a string no operator could paste anywhere.
	kmsDeniedKey       = regexp.MustCompile(`on resource: "?(arn:[^\s"]+)`)
	kmsDeniedPrincipal = regexp.MustCompile(`User: "?(arn:[^\s"]+)`)
)

// asKMSDenied recognises a KMS refusal inside an S3 error. It returns nil for
// everything else, including an AccessDenied that is about S3.
func asKMSDenied(err error) *KMSDeniedError {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "AccessDenied" {
		return nil
	}
	msg := apiErr.ErrorMessage()
	action := kmsDeniedAction.FindStringSubmatch(msg)
	if action == nil {
		return nil
	}
	out := &KMSDeniedError{Action: action[1], Err: err}
	if m := kmsDeniedKey.FindStringSubmatch(msg); m != nil {
		out.KeyARN = strings.TrimRight(m[1], ".")
	}
	if m := kmsDeniedPrincipal.FindStringSubmatch(msg); m != nil {
		out.Principal = m[1]
	}
	switch {
	case strings.Contains(msg, "explicit deny"):
		out.Where = KMSDeniedExplicitly
	case strings.Contains(msg, "no resource-based policy allows"):
		out.Where = KMSDeniedByKeyPolicy
	case strings.Contains(msg, "no identity-based policy allows"):
		out.Where = KMSDeniedByIdentityPolicy
	}
	return out
}

func (e *KMSDeniedError) Unwrap() error { return e.Err }

func (e *KMSDeniedError) Error() string {
	return fmt.Sprintf("%s %s S3 reports this as AccessDenied on its own operation, and the bucket and the S3 permissions may be entirely correct. (%v)",
		e.Headline(), e.Remedy(), e.Err)
}

// Headline is the one sentence of what happened: which key refused what to whom.
func (e *KMSDeniedError) Headline() string {
	key, who := "The bucket's KMS key", "this run's credentials"
	if e.KeyARN != "" {
		key += " " + e.KeyARN
	}
	if e.Principal != "" {
		who = e.Principal
	}
	return fmt.Sprintf("%s refused %s to %s.", key, e.Action, who)
}

// Remedy says where to look, which depends on the policy AWS blamed.
func (e *KMSDeniedError) Remedy() string {
	switch e.Where {
	case KMSDeniedByKeyPolicy:
		return "The key policy does not allow it: a customer managed key can be used only by the principals its key policy names, and the IAM policy on a role is not enough by itself. Add the estate's role to the key policy with kms:Decrypt and kms:GenerateDataKey."
	case KMSDeniedByIdentityPolicy:
		return "The IAM policy on the role does not allow it: it needs kms:Decrypt and kms:GenerateDataKey on this key. The published policy carries that statement when it is rendered with --kms and the key's ARN."
	case KMSDeniedExplicitly:
		return "A policy denies it explicitly, so no allow elsewhere helps: look for a Deny on this key in the key policy, the role's policies, a permissions boundary or an organization's service control policy."
	default:
		return "Check the key policy first (it must name the estate's role for kms:Decrypt and kms:GenerateDataKey), then the kms statement in the role's IAM policy."
	}
}

// KMSKeyUnusableError is an S3 request that failed because of the STATE of
// the bucket's KMS key rather than because of any policy. S3 relays those
// with the KMS exception's own name under a "KMS." prefix, on an HTTP 400:
// KMS.DisabledException, KMS.KMSInvalidStateException, KMS.NotFoundException.
//
// They are kept out of [KMSDeniedError] because every remedy that type offers
// is a policy edit and none of them helps here. A disabled key stays disabled
// however its key policy reads, and adding the estate's role to the policy of
// a key that is pending deletion still leaves the key pending deletion. An
// operator sent to the key policy for one of these loses the same afternoon
// KMSDeniedError exists to save, in the other direction. GitHub issue #1383.
type KMSKeyUnusableError struct {
	// Code is the code S3 relayed, for example "KMS.DisabledException".
	Code string
	// KeyARN is the key, when the message named one. Empty otherwise.
	KeyARN string
	// Err is the S3 error as it arrived.
	Err error
}

// asKMSKeyUnusable recognises a KMS key-state failure inside an S3 error.
// It returns nil for everything else.
//
// The code alone decides it. "KMS." is a prefix S3 puts on nothing but a
// relayed KMS exception, so there is no plain S3 failure to confuse it with
// and no need to also pin the status, which frees this from having to be
// re-measured if AWS ever answers one of these with something other than 400.
func asKMSKeyUnusable(err error) *KMSKeyUnusableError {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || !strings.HasPrefix(apiErr.ErrorCode(), "KMS.") {
		return nil
	}
	out := &KMSKeyUnusableError{Code: apiErr.ErrorCode(), Err: err}
	if m := kmsDeniedKey.FindStringSubmatch(apiErr.ErrorMessage()); m != nil {
		out.KeyARN = strings.TrimRight(m[1], ".")
	}
	return out
}

func (e *KMSKeyUnusableError) Unwrap() error { return e.Err }

func (e *KMSKeyUnusableError) Error() string {
	return fmt.Sprintf("%s %s S3 reports this on its own operation, and the bucket and every policy involved may be entirely correct. (%v)",
		e.Headline(), e.Remedy(), e.Err)
}

// Headline is the one sentence of what happened: which key could not be used.
func (e *KMSKeyUnusableError) Headline() string {
	key := "The bucket's KMS key"
	if e.KeyARN != "" {
		key += " " + e.KeyARN
	}
	return fmt.Sprintf("%s could not be used: KMS answered %s.", key, e.Code)
}

// Remedy says what to do, which depends on what KMS said.
func (e *KMSKeyUnusableError) Remedy() string {
	switch e.Code {
	case "KMS.DisabledException":
		return "The key is disabled. Enable it (kms:EnableKey, or the Enable button on the key in the console); no policy change makes a disabled key usable."
	case "KMS.KMSInvalidStateException":
		return "The key is in a state that does not allow this operation, which is most often pending deletion. Check the key's status: cancel the scheduled deletion (kms:CancelKeyDeletion) if that is what it is, and if the key is already gone, the objects encrypted under it cannot be read and the bucket needs a key that exists."
	case "KMS.NotFoundException":
		return "The key does not exist in this account and region. Check which key the bucket's default encryption names (choudoufu live-bucket says), that it is in the same region as the bucket, and that an alias in the configuration still resolves to a live key."
	default:
		return "Check the key's status and which key the bucket's default encryption names; this is the key's own state and not a permission, so a policy change will not move it."
	}
}

// s3OpError wraps a failed S3 call, naming a KMS refusal or a KMS key that
// cannot be used when that is what it is. Every error an [S3Store] operation
// returns from the wire goes through here, so the key is named whichever
// request met it first.
func s3OpError(doing, key string, err error) error {
	if denied := asKMSDenied(err); denied != nil {
		return fmt.Errorf("staterecord: s3: %s %q: %w", doing, key, denied)
	}
	if unusable := asKMSKeyUnusable(err); unusable != nil {
		return fmt.Errorf("staterecord: s3: %s %q: %w", doing, key, unusable)
	}
	return fmt.Errorf("staterecord: s3: %s %q: %w", doing, key, err)
}
