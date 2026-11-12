// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SweepResult is what [SSMSecretsStore.SweepOrphans] found and did.
type SweepResult struct {
	// Referenced is how many parameters under the namespace some record
	// references.
	Referenced int
	// Young is the unreferenced parameters left because they are inside
	// the grace period: a writer may be about to commit them.
	Young []string
	// Deleted is the orphans deleted.
	Deleted []string
}

// SweepOrphanSecretsAbortedError is a sweep that stopped before deleting
// anything, because its own run took longer than the bound its safety
// argument needs.
type SweepOrphanSecretsAbortedError struct {
	Elapsed, Bound time.Duration
}

func (e *SweepOrphanSecretsAbortedError) Error() string {
	return fmt.Sprintf("the orphan sweep took %s between reading the records and deleting, over the %s its safety allows; it deleted nothing and can be run again", e.Elapsed, e.Bound)
}

// SweepOrphans deletes the parameters under this store's namespace that no
// record references and that are older than the grace period: what a lost
// race, a failed commit or a failed superseded-delete leaves behind. It
// follows recordorphan_read.go's shape - list, then read every key before
// acting on it - and acts only on the SSM side; it never writes a record.
//
// # Why it never deletes a referenced parameter, a concurrent writer's included
//
// G is the grace period. Three bounds hold:
//
//  1. A writer commits within G/2 of creating its first new parameter, or
//     does not commit at all ([SecretWriteTooSlowError]).
//  2. This sweep deletes within G/2 of starting to read the records, or
//     deletes nothing ([SweepOrphanSecretsAbortedError]).
//  3. It deletes only a parameter last modified more than G before the
//     delete.
//
// Take a parameter P created at c and a sweep that starts reading records
// at s and deletes at d. By 3, c < d - G, and by 2, d - s < G/2, so
// c < s - G/2. Any record that references P either created it, and by 1
// committed before c + G/2 < s, or reused it from its own base record, in
// which case the version that first committed P did so before s and every
// version from then to this one references P - reuse is only ever from the
// base, and a version that dropped P could not be the base of one that
// references it. Every record is read after s, so whatever version the
// sweep reads of a key that references P at any time from s on references
// P too, and P is kept. A parameter created after c is younger than G and
// kept by 3 alone.
//
// Both clocks matter: 3 compares SSM's LastModifiedDate with this
// machine's clock, so a skew eats into the margin. G defaults to a day
// ([DefaultSSMSweepGrace]), against writes that take seconds.
//
// A record that cannot be read or decoded stops the sweep before it
// deletes anything: it may reference anything.
func (s *SSMSecretsStore) SweepOrphans(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	start := s.cfg.Now()

	keys, err := s.inner.List(ctx, s.cfg.RecordPrefix)
	if err != nil {
		return res, fmt.Errorf("orphan sweep: listing the records: %w", err)
	}
	referenced := map[string]bool{}
	for _, key := range keys {
		if !s.isRecordKey(key) {
			continue
		}
		payload, _, exists, err := s.inner.Get(ctx, key)
		if err != nil {
			return res, fmt.Errorf("orphan sweep: reading the record at %q: %w", key, err)
		}
		if !exists {
			continue
		}
		refs, err := s.sweepRefs(payload)
		if err != nil {
			return res, fmt.Errorf("orphan sweep: the record at %q: %w", key, err)
		}
		for _, r := range refs {
			referenced[r.Parameter] = true
		}
	}

	params, err := s.describeNamespace(ctx)
	if err != nil {
		return res, err
	}
	var orphans []string
	now := s.cfg.Now()
	for _, p := range params {
		name := aws.ToString(p.Name)
		if referenced[name] {
			res.Referenced++
			continue
		}
		if p.LastModifiedDate == nil || now.Sub(*p.LastModifiedDate) <= s.cfg.Grace {
			res.Young = append(res.Young, name)
			continue
		}
		orphans = append(orphans, name)
	}
	sort.Strings(res.Young)
	if len(orphans) == 0 {
		return res, nil
	}
	if elapsed, bound := s.cfg.Now().Sub(start), s.cfg.Grace/2; elapsed >= bound {
		return res, &SweepOrphanSecretsAbortedError{Elapsed: elapsed, Bound: bound}
	}
	deleted, err := s.deleteParameters(ctx, orphans)
	res.Deleted = deleted
	sort.Strings(res.Deleted)
	if err != nil {
		return res, fmt.Errorf("orphan sweep: deleting: %w", err)
	}
	return res, nil
}

// sweepRefs is the sweep's reading of a record's references.
func (s *SSMSecretsStore) sweepRefs(payload []byte) ([]secretRef, error) {
	return rawRefs(payload)
}

// describeNamespace lists every parameter under this store's namespace,
// metadata only: the sweep never decrypts.
func (s *SSMSecretsStore) describeNamespace(ctx context.Context) ([]ssmtypes.ParameterMetadata, error) {
	var out []ssmtypes.ParameterMetadata
	var token *string
	for {
		var resp *ssm.DescribeParametersOutput
		err := s.retry(ctx, "DescribeParameters", func() error {
			var err error
			resp, err = s.cfg.Client.DescribeParameters(ctx, &ssm.DescribeParametersInput{
				ParameterFilters: []ssmtypes.ParameterStringFilter{{
					Key:    aws.String("Path"),
					Option: aws.String("Recursive"),
					Values: []string{s.ns},
				}},
				MaxResults: aws.Int32(50),
				NextToken:  token,
			})
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("orphan sweep: listing parameters under %s: %w", s.ns, err)
		}
		out = append(out, resp.Parameters...)
		if aws.ToString(resp.NextToken) == "" {
			return out, nil
		}
		token = resp.NextToken
	}
}
