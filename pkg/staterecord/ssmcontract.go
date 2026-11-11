// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// The SSM contract. GitHub issue #2393, unit S2, for the design in #1515.
//
// Under `strict { secrets = "ssm" }` the values at a record's sensitive
// paths, and the provider's private bytes, go to Parameter Store as
// SecureString parameters under the key the ssm block names, and the record
// keeps a reference. Two things have to be true before the first value is
// written, and this file checks both on first contact, the way
// bucketcontract.go checks the bucket:
//
//   - customer_managed_key: kms_key_id resolves to a key whose KeyManager is
//     CUSTOMER, enabled, and symmetric. An AWS managed key, alias/aws/ssm
//     above all, is decryptable by every principal in the account holding
//     ssm:GetParameter, which is no narrower than the read on the bucket the
//     values are leaving. That one is refused by name, before any call, when
//     the configuration spells it, and refused again when DescribeKey
//     reports KeyManager AWS for a key ID or a customer alias that points at
//     one. Neither refusal is waivable: a waiver lets a run proceed without
//     a property being checked, and this is a property that was read and is
//     the one thing the arrangement exists to avoid.
//   - parameter_access: the identity can create a SecureString parameter
//     under the configured path with that key, read it back decrypted, and
//     delete it. The write path does all three - create the value, resolve
//     the reference on the next plan, delete a superseded value after the
//     record commits - so a role missing any one of them fails partway
//     through an apply rather than here.
//
// The probe parameter is created with Overwrite false under a random name
// no write path uses, and is deleted before this function returns. A probe
// left behind because the delete was denied is named in the finding.
//
// Like [CheckBucketContract], [CheckSSMContract] knows nothing about
// waivers. [AssertSSMContract] is the run's question: may it proceed.

// The SSM contract's settings, in the shared vocabulary [Setting]. They are
// the names an operator writes in the ssm block's allow_insecure, and
// internal/configs pins its list to [SSMSettings] by test.
const (
	SSMCustomerManagedKey Setting = "customer_managed_key"
	SSMParameterAccess    Setting = "parameter_access"
)

// SSMSettings is every asserted setting, in the order findings are reported.
var SSMSettings = []Setting{SSMCustomerManagedKey, SSMParameterAccess}

// SSMContractKMSAPI is the one KMS read the contract needs, costing
// kms:DescribeKey on the key. *kms.Client satisfies it.
type SSMContractKMSAPI interface {
	DescribeKey(ctx context.Context, in *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

// SSMContractAPI is the three Parameter Store calls the probe makes, costing
// ssm:PutParameter, ssm:GetParameters and ssm:DeleteParameter under the path,
// and kms:Encrypt and kms:Decrypt on the key, which SSM calls on the caller's
// behalf. *ssm.Client satisfies it.
type SSMContractAPI interface {
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParameters(ctx context.Context, in *ssm.GetParametersInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
	DeleteParameter(ctx context.Context, in *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// SSMProbeNamePrefix is the last path segment's prefix for the probe
// parameter. A write path naming its own parameters never starts a name with
// it, so an orphan sweep that finds one left behind may delete it.
const SSMProbeNamePrefix = "choudoufu-contract-probe-"

// ssmProbeValue is what the probe writes and expects to read back.
const ssmProbeValue = "choudoufu first-contact probe"

// newSSMProbeSuffix is swapped by tests for a fixed name.
var newSSMProbeSuffix = func() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AWSManagedKeyAlias reports whether keyID names an AWS managed key by its
// alias: "alias/aws/<service>", bare or inside an alias ARN. AWS reserves
// the alias/aws/ prefix for its own keys, so the spelling alone settles it.
// A key ID or a key ARN says nothing either way; DescribeKey answers those.
func AWSManagedKeyAlias(keyID string) bool {
	name := keyID
	if strings.HasPrefix(keyID, "arn:") {
		// arn:<partition>:kms:<region>:<account>:alias/aws/ssm
		parts := strings.SplitN(keyID, ":", 6)
		if len(parts) != 6 {
			return false
		}
		name = parts[5]
	}
	return strings.HasPrefix(name, "alias/aws/")
}

// CheckSSMContract reports one finding per [SSMSettings] entry, always both
// and always in that order, for the key keyID names and the parameter
// hierarchy path (absolute, with or without a trailing slash).
//
// The error return is for a failure that says nothing about the key or the
// role: a cancelled context, an unreachable endpoint. Any answer AWS gives
// is a finding.
func CheckSSMContract(ctx context.Context, ssmAPI SSMContractAPI, kmsAPI SSMContractKMSAPI, keyID, path string) ([]Finding, error) {
	k, err := checkSSMKey(ctx, kmsAPI, keyID)
	if err != nil {
		return nil, err
	}
	p, err := checkSSMParameterAccess(ctx, ssmAPI, keyID, path)
	if err != nil {
		return nil, err
	}
	return []Finding{k, p}, nil
}

func checkSSMKey(ctx context.Context, api SSMContractKMSAPI, keyID string) (Finding, error) {
	f := Finding{Setting: SSMCustomerManagedKey}
	if AWSManagedKeyAlias(keyID) {
		f.Unwaivable = true
		f.Found = fmt.Sprintf("kms_key_id %q is an AWS managed key by its alias", keyID)
		return f, nil
	}
	out, err := api.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		switch {
		case accessDenied(err):
			f.Outcome = Unreadable
			f.Found = fmt.Sprintf("kms:DescribeKey on %q was denied", keyID)
			return f, nil
		case apiErrorCode(err) == "NotFoundException":
			f.Found = fmt.Sprintf("no KMS key %q was found in this account and region", keyID)
			return f, nil
		case apiErrorCode(err) != "":
			f.Found = fmt.Sprintf("kms:DescribeKey on %q answered %s", keyID, apiErrorCode(err))
			return f, nil
		}
		return f, fmt.Errorf("staterecord: ssm: describing KMS key %q: %w", keyID, err)
	}
	md := out.KeyMetadata
	if md == nil {
		f.Found = fmt.Sprintf("kms:DescribeKey on %q returned no key metadata", keyID)
		return f, nil
	}
	arn := aws.ToString(md.Arn)
	switch {
	case md.KeyManager == kmstypes.KeyManagerTypeAws:
		f.Unwaivable = true
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, whose KeyManager is AWS", keyID, arn)
	case md.KeyManager != kmstypes.KeyManagerTypeCustomer:
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, whose KeyManager is %q rather than CUSTOMER", keyID, arn, md.KeyManager)
	case md.KeyState != kmstypes.KeyStateEnabled:
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, which is %s", keyID, arn, md.KeyState)
	case md.KeySpec != "" && md.KeySpec != kmstypes.KeySpecSymmetricDefault:
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, whose key spec is %s; SecureString needs a SYMMETRIC_DEFAULT key", keyID, arn, md.KeySpec)
	case md.KeyUsage != "" && md.KeyUsage != kmstypes.KeyUsageTypeEncryptDecrypt:
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, whose key usage is %s rather than ENCRYPT_DECRYPT", keyID, arn, md.KeyUsage)
	default:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("kms_key_id %q resolves to %s, a customer managed key", keyID, arn)
	}
	return f, nil
}

// checkSSMParameterAccess creates, reads back and deletes one probe
// parameter. The delete is attempted whenever the create succeeded, so a
// failed read leaves nothing behind either.
func checkSSMParameterAccess(ctx context.Context, api SSMContractAPI, keyID, path string) (Finding, error) {
	f := Finding{Setting: SSMParameterAccess}
	name := strings.TrimSuffix(path, "/") + "/" + SSMProbeNamePrefix + newSSMProbeSuffix()

	_, err := api.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(ssmProbeValue),
		Type:      ssmtypes.ParameterTypeSecureString,
		KeyId:     aws.String(keyID),
		Overwrite: aws.Bool(false),
		Tier:      ssmtypes.ParameterTierStandard,
	})
	if err != nil {
		if apiErrorCode(err) == "" {
			return f, fmt.Errorf("staterecord: ssm: creating probe parameter %q: %w", name, err)
		}
		f.Found = fmt.Sprintf("ssm:PutParameter of SecureString %q under key %q answered %s", name, keyID, ssmErrorClause(err))
		return f, nil
	}

	readFound, readErr := ssmProbeRead(ctx, api, name)

	_, delErr := api.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String(name)})
	if readErr != nil {
		return f, readErr
	}
	if delErr != nil && apiErrorCode(delErr) == "" {
		return f, fmt.Errorf("staterecord: ssm: deleting probe parameter %q: %w", name, delErr)
	}

	var problems []string
	if readFound != "" {
		problems = append(problems, readFound)
	}
	if delErr != nil {
		problems = append(problems, fmt.Sprintf("ssm:DeleteParameter of %q answered %s, so the probe parameter is still there and can be deleted by hand", name, ssmErrorClause(delErr)))
	}
	if len(problems) > 0 {
		f.Found = strings.Join(problems, "; and ")
		return f, nil
	}
	f.Outcome = Passed
	f.Found = fmt.Sprintf("a SecureString parameter under %s was created, read back decrypted and deleted", strings.TrimSuffix(path, "/"))
	return f, nil
}

// ssmProbeRead reads the probe back with decryption. It returns a clause for
// the finding when the read answered something wrong, and an error only when
// it got no answer at all.
func ssmProbeRead(ctx context.Context, api SSMContractAPI, name string) (string, error) {
	out, err := api.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          []string{name},
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if apiErrorCode(err) == "" {
			return "", fmt.Errorf("staterecord: ssm: reading probe parameter %q: %w", name, err)
		}
		return fmt.Sprintf("ssm:GetParameters with decryption of %q answered %s", name, ssmErrorClause(err)), nil
	}
	if len(out.Parameters) != 1 {
		return fmt.Sprintf("ssm:GetParameters did not return %q straight after it was created", name), nil
	}
	if got := aws.ToString(out.Parameters[0].Value); got != ssmProbeValue {
		return fmt.Sprintf("ssm:GetParameters returned %q with a value other than the one written, so it was not decrypted", name), nil
	}
	return "", nil
}

// ssmErrorClause names an AWS error by its code and message, the way a
// finding quotes it.
func ssmErrorClause(err error) string {
	code := apiErrorCode(err)
	msg := err.Error()
	var apiErr interface{ ErrorMessage() string }
	if errors.As(err, &apiErr) && apiErr.ErrorMessage() != "" {
		msg = apiErr.ErrorMessage()
	}
	return fmt.Sprintf("%s (%s)", code, msg)
}

// SSMContractRefusal is the headline and the paragraph for one finding that
// did not pass, in internal/command's liveCommandRefusals shape. Empty for a
// finding that passed.
func SSMContractRefusal(f Finding) (summary, detail string) {
	if f.OK() {
		return "", ""
	}
	switch f.Setting {
	case SSMCustomerManagedKey:
		if f.Unwaivable {
			return "The ssm block names an AWS managed key",
				fmt.Sprintf("%s.\n\nAn AWS managed key such as alias/aws/ssm can be used by every principal in the account that holds ssm:GetParameter, which is no narrower than the read on the bucket the secret values are being moved out of. secrets = \"ssm\" requires a customer managed key, whose key policy is a second gate on the values. The ssm block's allow_insecure does not cover this.\n\nCreate a symmetric customer managed key, grant this role kms:Encrypt, kms:GenerateDataKey and kms:Decrypt in its key policy, and name it in kms_key_id.", f.Found)
		}
		why := "secrets = \"ssm\" encrypts every secret value under this key, and the arrangement depends on it being a customer managed key: its key policy is the second gate on the values, and disabling it revokes them without going through IAM."
		if f.Outcome == Unreadable {
			return "The ssm block's KMS key could not be checked",
				fmt.Sprintf("%s, so whether it is a customer managed key is unknown, and a key nobody could check is refused the same as one that failed.\n\n%s\n\nGrant the role kms:DescribeKey on the key, or waive this check with allow_insecure = [\"%s\"] in the ssm block.", f.Found, why, SSMCustomerManagedKey)
		}
		return "The ssm block's KMS key fails its customer_managed_key assertion",
			fmt.Sprintf("%s.\n\n%s\n\nName an enabled, symmetric customer managed key in kms_key_id.", f.Found, why)
	case SSMParameterAccess:
		return "This role cannot keep secret values under the ssm block's path",
			fmt.Sprintf("%s.\n\nUnder secrets = \"ssm\" a run creates a SecureString parameter for each secret value, reads them back on every plan, and deletes a superseded one after the record commits. The role needs ssm:PutParameter, ssm:GetParameters, ssm:DeleteParameter and ssm:GetParametersByPath on the path, and kms:Encrypt, kms:GenerateDataKey and kms:Decrypt on the key through SSM; examples/record-store-bucket/iam/render-policy.sh --ssm-path --ssm-kms renders them. Nothing has been written to the record store.", f.Found)
	}
	return fmt.Sprintf("The ssm contract fails its %s assertion", f.Setting), f.Found + "."
}

// SSMWaiverCost says what an estate gives up by waiving setting, as a clause
// that completes "is waived, so ...", the shape [BucketWaiverCost] has.
func SSMWaiverCost(setting Setting) string {
	switch setting {
	case SSMCustomerManagedKey:
		return "nothing has checked that kms_key_id is an enabled customer managed key; an AWS managed key named by its alias, or found by DescribeKey, is refused all the same"
	case SSMParameterAccess:
		return "nothing has checked that this role can create, read back and delete parameters under the path, so a run may fail at its first secret write, and a role that cannot delete leaves superseded values counting against the account's 10,000 standard parameters"
	}
	return "that assertion is not made"
}

// SSMContractRefusalError is a first-contact refusal of the SSM arrangement.
// Nothing has been written to Parameter Store or to the record store when
// one is returned.
type SSMContractRefusalError struct {
	// Findings are the refused findings, in [SSMSettings] order.
	Findings []Finding
}

func (e *SSMContractRefusalError) Error() string {
	var b strings.Builder
	for i, f := range e.Findings {
		if i > 0 {
			b.WriteString("\n\n")
		}
		summary, detail := SSMContractRefusal(f)
		b.WriteString(summary)
		b.WriteString(". ")
		b.WriteString(detail)
	}
	return b.String()
}

// AssertSSMContract is the first-contact call for the store that writes
// secret values: it checks the contract and decides whether a run may
// proceed. waived is the ssm block's allow_insecure.
//
// It returns an [*SSMContractRefusalError] when any finding is refused, an
// ordinary error when the check could not be made, and otherwise the
// findings the waiver let through, which the caller warns about by name with
// [SSMWaiverCost].
func AssertSSMContract(ctx context.Context, ssmAPI SSMContractAPI, kmsAPI SSMContractKMSAPI, keyID, path string, waived []string) (waivedFailing []Finding, err error) {
	findings, err := CheckSSMContract(ctx, ssmAPI, kmsAPI, keyID, path)
	if err != nil {
		return nil, err
	}
	// The SSM contract reports no NotChecked or Warned outcome, so
	// SplitWaived's middle return is always empty here.
	refused, _, waivedFailing := SplitWaived(findings, waived)
	if len(refused) > 0 {
		return nil, &SSMContractRefusalError{Findings: refused}
	}
	return waivedFailing, nil
}
