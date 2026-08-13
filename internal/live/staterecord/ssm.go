// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SSMStore is a [Store] backed by AWS Systems Manager Parameter Store —
// the zero-infrastructure team default: no bucket to create, nothing
// beyond IAM to provision.
//
// # What SSM actually offers, investigated against its real API surface
//
// Parameter Store's only server-enforced condition is PutParameter's
// Overwrite flag, a bare boolean with no version attached to it. That is
// real, atomic create-only CAS, and this store uses it exactly that way:
// [SSMStore.PutIfAbsent] and a "" [SSMStore.PutIfVersion] call send
// Overwrite: false, and a create racing an existing parameter fails
// atomically with ParameterAlreadyExists — no read-compare-write window,
// no weaker-race caveat, the same strength [S3Store] offers for creation.
//
// Updating a specific version is a different story, and this is the
// honest limit: there is no "overwrite if the current version is N"
// primitive anywhere in the PutParameter API — Overwrite is boolean, not
// versioned. So [SSMStore.PutIfVersion] on an existing key is a
// read-compare-write: GetParameter to read the current Version, a
// client-side compare against expectedVersion, then PutParameter with
// Overwrite: true if they match. Between that read and that write, a
// second caller's PutParameter can land — SSM has nothing that would
// reject it — and this store's write then silently overwrites it. The one
// honest mitigation available is best-effort detection after the fact:
// PutParameterOutput.Version reports the version the write just created,
// and if that is not exactly expectedVersion+1, some other write landed
// in the window. This store checks that and returns a
// *[VersionConflictError] when it does not line up — but by then the
// write has already happened. The conflict is reported, not prevented:
// weaker than [LocalStore] or [S3Store], where a conflicting write never
// reaches the backend's stored state at all.
//
// [SSMStore.Delete] is weaker again: DeleteParameter takes only a Name,
// no version-shaped parameter whatsoever, and returns nothing an
// after-the-fact check could compare. This store still performs a
// read-compare-delete for the loud-failure behavior a caller expects
// going in, but a write that lands between the read and the delete is
// invisible to it — there is no returned value here analogous to
// PutParameterOutput.Version, so unlike PutIfVersion, a Delete race is not
// even detected after the fact. Teams that need real delete CAS want
// [S3Store].
//
// # Payload encoding
//
// Parameter Store values are UTF-8 strings, not bytes, so this store
// base64-encodes payload before calling PutParameter and decodes it back
// in Get — invisible to a [Store] caller (opaque []byte in, the identical
// []byte out), but worth knowing before reading a parameter's Value
// directly in the AWS console or CLI: it is base64, not the raw payload.
//
// # List's approximation
//
// GetParametersByPath — the only enumeration primitive Parameter Store
// has — matches whole "/"-delimited hierarchy segments, not an arbitrary
// string prefix: a path of "/foo" matches "/foo/bar" but not "/foobar",
// where [Store.List]'s contract is an ordinary string prefix and would
// expect the latter too. [SSMStore.List] asks GetParametersByPath for the
// nearest enclosing hierarchy folder (recursively) and then filters the
// result down to keyPrefix itself with a plain string comparison, so the
// contract holds; it is one broader read than the strictly minimal one,
// not an incorrect one.
type SSMStore struct {
	client    *ssm.Client
	keyPrefix string
}

// SSMConfig configures an [SSMStore].
type SSMConfig struct {
	// Client is the SSM client every call goes through. The caller builds
	// and authenticates it; this package has no opinion on region,
	// credentials, or any endpoint override.
	Client *ssm.Client

	// KeyPrefix is the parameter-name hierarchy every key lives under,
	// e.g. "/myteam/mystate". A leading "/" is added if missing; a
	// trailing one is trimmed. Every key this store is asked for becomes
	// the parameter name KeyPrefix + "/" + key. This package does not
	// interpret KeyPrefix beyond that join — it is an opaque string, the
	// same as every key passed to the [Store] interface.
	KeyPrefix string
}

// NewSSMStore builds an [SSMStore] from cfg.
func NewSSMStore(cfg SSMConfig) (*SSMStore, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("staterecord: ssm: Client must not be nil")
	}
	prefix := strings.TrimSuffix(cfg.KeyPrefix, "/")
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return &SSMStore{client: cfg.Client, keyPrefix: prefix}, nil
}

// parameterName joins s.keyPrefix and key into the parameter name sent to
// SSM.
func (s *SSMStore) parameterName(key string) string {
	return s.keyPrefix + "/" + strings.TrimPrefix(key, "/")
}

// keyFromParameterName reverses parameterName, for turning
// GetParametersByPath results back into the opaque keys [Store.List]
// promises.
func (s *SSMStore) keyFromParameterName(name string) string {
	return strings.TrimPrefix(name, s.keyPrefix+"/")
}

// Get implements [Store].
func (s *SSMStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(s.parameterName(key)),
	})
	if err != nil {
		var notFound *types.ParameterNotFound
		if errors.As(err, &notFound) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("staterecord: ssm: getting %q: %w", key, err)
	}
	payload, err := base64.StdEncoding.DecodeString(aws.ToString(out.Parameter.Value))
	if err != nil {
		return nil, "", false, fmt.Errorf("staterecord: ssm: decoding the value stored for %q: %w", key, err)
	}
	return payload, strconv.FormatInt(out.Parameter.Version, 10), true, nil
}

// PutIfAbsent implements [Store], via PutParameter's Overwrite: false —
// real atomic create-only CAS, not a read-compare-write.
func (s *SSMStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	out, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(s.parameterName(key)),
		Value:     aws.String(base64.StdEncoding.EncodeToString(payload)),
		Type:      types.ParameterTypeString,
		Overwrite: aws.Bool(false),
	})
	if err != nil {
		var already *types.ParameterAlreadyExists
		if errors.As(err, &already) {
			_, actual, exists, gerr := s.Get(ctx, key)
			if gerr != nil {
				return "", gerr
			}
			av := ""
			if exists {
				av = actual
			}
			return "", &VersionConflictError{Key: key, ExpectedVersion: "", ActualVersion: av}
		}
		return "", fmt.Errorf("staterecord: ssm: creating %q: %w", key, err)
	}
	return strconv.FormatInt(out.Version, 10), nil
}

// PutIfVersion implements [Store]. expectedVersion == "" delegates to
// [SSMStore.PutIfAbsent]. Any other value is the read-compare-write this
// type's doc comment describes in full — read the "What SSM actually
// offers" section before relying on this for anything where a lost update
// would matter.
func (s *SSMStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if expectedVersion == "" {
		return s.PutIfAbsent(ctx, key, payload)
	}
	if err := validateKey(key); err != nil {
		return "", err
	}

	_, currentVersion, exists, err := s.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: ""}
	}
	if currentVersion != expectedVersion {
		return "", &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: currentVersion}
	}

	// currentVersion == expectedVersion here, and currentVersion is always
	// what Get formatted from a real Parameter.Version via
	// strconv.FormatInt, so this is guaranteed to parse — expectedVersion
	// only ever reaches this point already proven to be a version this
	// store itself issued.
	wantVersion, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil {
		return "", fmt.Errorf("staterecord: ssm: internal error: current version %q did not parse: %w", expectedVersion, err)
	}

	out, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(s.parameterName(key)),
		Value:     aws.String(base64.StdEncoding.EncodeToString(payload)),
		Type:      types.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("staterecord: ssm: writing %q: %w", key, err)
	}

	// Best-effort detection AFTER the fact: the write has already
	// happened by this point regardless of what this check finds — see
	// the type doc. A version other than wantVersion+1 means a second
	// writer's PutParameter landed inside the read-compare-write window.
	if out.Version != wantVersion+1 {
		return "", &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: strconv.FormatInt(out.Version, 10)}
	}
	return strconv.FormatInt(out.Version, 10), nil
}

// Delete implements [Store] as a read-compare-delete: SSM's
// DeleteParameter has no conditional form at all (see the type doc), so
// this is the same race-window caveat [SSMStore.PutIfVersion] carries,
// minus even the after-the-fact detection PutParameterOutput.Version
// gives that call — DeleteParameter returns nothing to compare.
func (s *SSMStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, currentVersion, exists, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		if expectedVersion == "" {
			return nil
		}
		return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: ""}
	}
	if currentVersion != expectedVersion {
		return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: currentVersion}
	}
	_, err = s.client.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(s.parameterName(key)),
	})
	if err != nil {
		var notFound *types.ParameterNotFound
		if errors.As(err, &notFound) {
			// Raced away between the read above and this call — not this
			// call's version to complain about, but still not the success
			// the caller asked for.
			return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: ""}
		}
		return fmt.Errorf("staterecord: ssm: deleting %q: %w", key, err)
	}
	return nil
}

// List implements [Store]. See the type doc's "List's approximation"
// section: GetParametersByPath matches hierarchy segments, not a bare
// string prefix, so this asks for the nearest enclosing folder and
// filters client-side down to the interface's actual contract.
func (s *SSMStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	folder := s.keyPrefix
	if i := strings.LastIndex(keyPrefix, "/"); i >= 0 {
		folder = s.parameterName(keyPrefix[:i])
	}

	var keys []string
	var token *string
	for {
		out, err := s.client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String(folder),
			Recursive: aws.Bool(true),
			NextToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("staterecord: ssm: listing %q: %w", keyPrefix, err)
		}
		for _, p := range out.Parameters {
			key := s.keyFromParameterName(aws.ToString(p.Name))
			if strings.HasPrefix(key, keyPrefix) {
				keys = append(keys, key)
			}
		}
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		token = out.NextToken
	}
	sort.Strings(keys)
	return keys, nil
}
