// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/json"
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #1515's capacity refusal, #1309's carried over.
// #1309's CheckRecordCapacity was retired with the Parameter Store record
// backend (#1346) because its count was records; here the count is secret
// parameters, and the arithmetic and the bands are #1309's:
//
//   - Over the tier's ceiling: refuse, before the first parameter is
//     written. It cannot succeed in an empty account at any hour.
//   - Nine tenths of the way there: warn. The ceiling is an account and
//     region total, so whether it fits depends on parameters this store
//     does not count, and a refusal built on a guess would turn away
//     estates that fit.
//
// The count is what this estate's records will reference: one parameter
// per sensitive path, per provider private, per sensitive residue value.

// SummarySecretsCapacityExceeded and SummarySecretsCapacityNear are the two
// diagnostics' summaries.
const (
	SummarySecretsCapacityExceeded = "Too many secret parameters for SSM"
	SummarySecretsCapacityNear     = "Secret parameters close to SSM's ceiling"
)

// secretsWarnFraction is #1309's warning band.
const secretsWarnFraction = 0.9

// SecretParameterCount is a number of parameters by tier.
type SecretParameterCount struct {
	Standard int
	Advanced int
}

// CheckSecretsCapacity is the plan-time check: an error diagnostic when
// either tier's count is over its ceiling, a warning in the band below.
func CheckSecretsCapacity(planned SecretParameterCount) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, t := range []struct {
		name    string
		n, ceil int
	}{
		{"standard", planned.Standard, SSMStandardParameterCeiling},
		{"advanced", planned.Advanced, SSMAdvancedParameterCeiling},
	} {
		switch {
		case t.n > t.ceil:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummarySecretsCapacityExceeded, fmt.Sprintf(
				"This estate's records would reference %d %s-tier SSM parameters, and an AWS account holds at most %d per region. Nothing was written: a run that reached the ceiling partway would leave records committed for some secrets and not others. Keep fewer secret values in SSM (strict { secrets = \"refuse\" } for some of them, or a separate estate in another region), or keep them in the record store.",
				t.n, t.name, t.ceil)))
		case float64(t.n) >= float64(t.ceil)*secretsWarnFraction:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummarySecretsCapacityNear, fmt.Sprintf(
				"This estate's records will reference %d %s-tier SSM parameters, and an AWS account holds at most %d per region, counting every other parameter in it. Whether these fit depends on how many the account already holds, which this run does not read.",
				t.n, t.name, t.ceil)))
		}
	}
	return diags
}

// SecretsCapacityError is the refusal the store raises before its first
// parameter write when the count is over a ceiling.
type SecretsCapacityError struct {
	Diags tfdiags.Diagnostics
}

func (e *SecretsCapacityError) Error() string { return e.Diags.Err().Error() }

// PlanSecretParameters hands the store the count a plan computed for the
// run, which the first write checks. Without one, the first write counts
// what the estate's records already reference (from the run's bulk read)
// and adds its own new parameters, which is a lower bound.
func (s *SSMSecretsStore) PlanSecretParameters(n SecretParameterCount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planned = &n
}

// admitCapacity runs the capacity check once, before the run's first
// parameter is created, and refuses every write of the run from then on if
// it fails: a refusal before the first write is only that if it holds for
// the second.
func (s *SSMSecretsStore) admitCapacity(create []pendingSecret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capacityDone {
		return s.capacityErr
	}
	s.capacityDone = true
	var n SecretParameterCount
	if s.planned != nil {
		n = *s.planned
	} else {
		for _, tier := range s.tiers {
			if tier == secretTierAdvancedLabel {
				n.Advanced++
			} else {
				n.Standard++
			}
		}
		for _, c := range create {
			if c.ref.Tier == secretTierAdvancedLabel {
				n.Advanced++
			} else {
				n.Standard++
			}
		}
	}
	diags := CheckSecretsCapacity(n)
	if diags.HasErrors() {
		s.capacityErr = &SecretsCapacityError{Diags: diags}
	}
	return s.capacityErr
}

// CountSecretParameters is the count [CheckSecretsCapacity] takes, for a
// set of record payloads as the store would be handed them.
func CountSecretParameters(payloads [][]byte) (SecretParameterCount, error) {
	var n SecretParameterCount
	for _, p := range payloads {
		var env recordEnvelope
		if err := json.Unmarshal(p, &env); err != nil {
			return n, err
		}
		pending, err := splitSecrets(&env)
		if err != nil {
			return n, err
		}
		for _, ps := range pending {
			if len(ps.value) > ssmStandardMaxBytes {
				n.Advanced++
			} else {
				n.Standard++
			}
		}
	}
	return n, nil
}
