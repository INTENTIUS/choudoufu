// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// fakeKMS answers DescribeKey from a table keyed by the KeyId asked for.
type fakeKMS struct {
	keys  map[string]kmstypes.KeyMetadata
	err   error
	calls []string
}

func (f *fakeKMS) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	f.calls = append(f.calls, aws.ToString(in.KeyId))
	if f.err != nil {
		return nil, f.err
	}
	md, ok := f.keys[aws.ToString(in.KeyId)]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NotFoundException", Message: "key not found"}
	}
	return &kms.DescribeKeyOutput{KeyMetadata: &md}, nil
}

// fakeSSM is a parameter store with one map and an injectable error per
// operation. A SecureString value is "encrypted" by prefixing it, so a read
// without decryption is visibly wrong.
type fakeSSM struct {
	params map[string]string

	putErr, getErr, delErr error
	// noDecrypt returns the stored ciphertext even when decryption is asked
	// for, the answer a role holding ssm:GetParameters without kms:Decrypt
	// would never actually get, kept to prove the value is compared.
	noDecrypt bool

	puts []ssm.PutParameterInput
}

func newFakeSSM() *fakeSSM { return &fakeSSM{params: map[string]string{}} }

func (f *fakeSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.puts = append(f.puts, *in)
	if f.putErr != nil {
		return nil, f.putErr
	}
	name := aws.ToString(in.Name)
	if _, exists := f.params[name]; exists && !aws.ToBool(in.Overwrite) {
		return nil, &smithy.GenericAPIError{Code: "ParameterAlreadyExists"}
	}
	f.params[name] = "enc:" + aws.ToString(in.Value)
	return &ssm.PutParameterOutput{Version: 1}, nil
}

func (f *fakeSSM) GetParameters(_ context.Context, in *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := &ssm.GetParametersOutput{}
	for _, n := range in.Names {
		v, ok := f.params[n]
		if !ok {
			out.InvalidParameters = append(out.InvalidParameters, n)
			continue
		}
		if aws.ToBool(in.WithDecryption) && !f.noDecrypt {
			v = strings.TrimPrefix(v, "enc:")
		}
		out.Parameters = append(out.Parameters, ssmtypes.Parameter{Name: aws.String(n), Value: aws.String(v)})
	}
	return out, nil
}

func (f *fakeSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	if f.delErr != nil {
		return nil, f.delErr
	}
	delete(f.params, aws.ToString(in.Name))
	return &ssm.DeleteParameterOutput{}, nil
}

const (
	testCMK    = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	testAWSKey = "arn:aws:kms:eu-west-1:111122223333:key/aaaa0000-0000-0000-0000-000000000000"
	testPath   = "/choudoufu/prod/secrets"
)

func goodKMS() *fakeKMS {
	return &fakeKMS{keys: map[string]kmstypes.KeyMetadata{
		testCMK: {
			Arn: aws.String(testCMK), KeyManager: kmstypes.KeyManagerTypeCustomer,
			KeyState: kmstypes.KeyStateEnabled, KeySpec: kmstypes.KeySpecSymmetricDefault,
			KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
		},
		"alias/team-secrets": {
			Arn: aws.String(testCMK), KeyManager: kmstypes.KeyManagerTypeCustomer,
			KeyState: kmstypes.KeyStateEnabled, KeySpec: kmstypes.KeySpecSymmetricDefault,
			KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
		},
		// An AWS managed key reached by its key ARN, which says nothing
		// about who manages it until DescribeKey answers.
		testAWSKey: {
			Arn: aws.String(testAWSKey), KeyManager: kmstypes.KeyManagerTypeAws,
			KeyState: kmstypes.KeyStateEnabled, KeySpec: kmstypes.KeySpecSymmetricDefault,
			KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
		},
		"alias/disabled": {
			Arn: aws.String(testCMK), KeyManager: kmstypes.KeyManagerTypeCustomer,
			KeyState: kmstypes.KeyStateDisabled, KeySpec: kmstypes.KeySpecSymmetricDefault,
			KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
		},
		"alias/asymmetric": {
			Arn: aws.String(testCMK), KeyManager: kmstypes.KeyManagerTypeCustomer,
			KeyState: kmstypes.KeyStateEnabled, KeySpec: kmstypes.KeySpecRsa2048,
			KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
		},
	}}
}

func fixedProbeName(t *testing.T) {
	t.Helper()
	prev := newSSMProbeSuffix
	newSSMProbeSuffix = func() string { return "0123456789abcdef" }
	t.Cleanup(func() { newSSMProbeSuffix = prev })
}

func ssmFindingFor(t *testing.T, findings []Finding, s Setting) Finding {
	t.Helper()
	if len(findings) != len(SSMSettings) {
		t.Fatalf("got %d findings, want one per setting %v: %+v", len(findings), SSMSettings, findings)
	}
	for i, f := range findings {
		if f.Setting != SSMSettings[i] {
			t.Fatalf("finding %d is %q, want %q: findings must come in SSMSettings order", i, f.Setting, SSMSettings[i])
		}
	}
	for _, f := range findings {
		if f.Setting == s {
			return f
		}
	}
	t.Fatalf("no finding for %q", s)
	return Finding{}
}

func TestSSMContractCustomerManagedKeyPasses(t *testing.T) {
	fixedProbeName(t)
	for _, keyID := range []string{testCMK, "alias/team-secrets"} {
		t.Run(keyID, func(t *testing.T) {
			s := newFakeSSM()
			findings, err := CheckSSMContract(t.Context(), s, goodKMS(), keyID, testPath+"/")
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if !f.OK() {
					t.Errorf("%s did not pass: %s", f.Setting, f.Found)
				}
			}
			// The probe was a SecureString under the configured key, a
			// create rather than an overwrite, and it is gone afterwards.
			if len(s.puts) != 1 {
				t.Fatalf("PutParameter called %d times, want 1", len(s.puts))
			}
			p := s.puts[0]
			if got, want := aws.ToString(p.Name), testPath+"/"+SSMProbeNamePrefix+"0123456789abcdef"; got != want {
				t.Errorf("probe name = %q, want %q", got, want)
			}
			if p.Type != ssmtypes.ParameterTypeSecureString || aws.ToString(p.KeyId) != keyID || aws.ToBool(p.Overwrite) {
				t.Errorf("probe put = type %s key %q overwrite %v, want SecureString under %q with Overwrite false", p.Type, aws.ToString(p.KeyId), aws.ToBool(p.Overwrite), keyID)
			}
			if len(s.params) != 0 {
				t.Errorf("the probe was left behind: %v", s.params)
			}
			waived, err := AssertSSMContract(t.Context(), newFakeSSM(), goodKMS(), keyID, testPath, nil)
			if err != nil || len(waived) != 0 {
				t.Errorf("AssertSSMContract = %v, %v; want a clean pass", waived, err)
			}
		})
	}
}

// TestSSMContractRefusesAWSManagedKeys: alias/aws/ssm is refused by its
// spelling, before KMS is asked anything, and a key that DescribeKey reports
// as AWS managed is refused too. Neither is waivable.
func TestSSMContractRefusesAWSManagedKeys(t *testing.T) {
	fixedProbeName(t)
	for _, tc := range []struct {
		keyID     string
		wantCall  bool
		wantFound string
	}{
		{keyID: "alias/aws/ssm", wantFound: `"alias/aws/ssm" is an AWS managed key by its alias`},
		{keyID: "arn:aws:kms:eu-west-1:111122223333:alias/aws/ssm", wantFound: "AWS managed key by its alias"},
		{keyID: "alias/aws/s3", wantFound: `"alias/aws/s3" is an AWS managed key`},
		{keyID: testAWSKey, wantCall: true, wantFound: "whose KeyManager is AWS"},
	} {
		t.Run(tc.keyID, func(t *testing.T) {
			k := goodKMS()
			findings, err := CheckSSMContract(t.Context(), newFakeSSM(), k, tc.keyID, testPath)
			if err != nil {
				t.Fatal(err)
			}
			f := ssmFindingFor(t, findings, SSMCustomerManagedKey)
			if f.OK() || !f.Unwaivable {
				t.Fatalf("finding = %+v, want an unwaivable failure", f)
			}
			if !strings.Contains(f.Found, tc.wantFound) {
				t.Errorf("Found = %q, want it to contain %q", f.Found, tc.wantFound)
			}
			if called := len(k.calls) > 0; called != tc.wantCall {
				t.Errorf("DescribeKey called = %v, want %v", called, tc.wantCall)
			}

			// The waiver that names the setting does not reach it.
			_, err = AssertSSMContract(t.Context(), newFakeSSM(), goodKMS(), tc.keyID, testPath, []string{string(SSMCustomerManagedKey)})
			var refusal *SSMContractRefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("AssertSSMContract with the waiver = %v, want a refusal", err)
			}
			for _, want := range []string{"AWS managed key", tc.keyID, "allow_insecure does not cover this"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not contain %q: %s", want, err)
				}
			}
		})
	}
}

func TestAWSManagedKeyAlias(t *testing.T) {
	for keyID, want := range map[string]bool{
		"alias/aws/ssm": true,
		"arn:aws:kms:us-east-1:111122223333:alias/aws/ssm":            true,
		"arn:aws-us-gov:kms:us-gov-west-1:111122223333:alias/aws/ebs": true,
		"alias/awsome":       false,
		"alias/team/aws/ssm": false,
		testCMK:              false,
		"1234abcd-12ab-34cd": false,
		"arn:aws:kms:us-east-1:111122223333:alias/team": false,
	} {
		if got := AWSManagedKeyAlias(keyID); got != want {
			t.Errorf("AWSManagedKeyAlias(%q) = %v, want %v", keyID, got, want)
		}
	}
}

// TestSSMContractKeyFailures: the other ways a key fails are ordinary,
// waivable failures, and a denied DescribeKey is Unreadable rather than a
// pass.
func TestSSMContractKeyFailures(t *testing.T) {
	fixedProbeName(t)
	for _, tc := range []struct {
		name, keyID string
		kmsErr      error
		outcome     Outcome
		wantFound   string
	}{
		{name: "disabled", keyID: "alias/disabled", outcome: Failed, wantFound: "which is Disabled"},
		{name: "asymmetric", keyID: "alias/asymmetric", outcome: Failed, wantFound: "SYMMETRIC_DEFAULT"},
		{name: "missing", keyID: "alias/nope", outcome: Failed, wantFound: "no KMS key"},
		{name: "denied", keyID: testCMK, kmsErr: &smithy.GenericAPIError{Code: "AccessDeniedException"}, outcome: Unreadable, wantFound: "kms:DescribeKey on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := goodKMS()
			k.err = tc.kmsErr
			findings, err := CheckSSMContract(t.Context(), newFakeSSM(), k, tc.keyID, testPath)
			if err != nil {
				t.Fatal(err)
			}
			f := ssmFindingFor(t, findings, SSMCustomerManagedKey)
			if f.Outcome != tc.outcome || f.Unwaivable {
				t.Errorf("finding = %+v, want outcome %d and waivable", f, tc.outcome)
			}
			if !strings.Contains(f.Found, tc.wantFound) {
				t.Errorf("Found = %q, want it to contain %q", f.Found, tc.wantFound)
			}
		})
	}

	t.Run("a transport failure is an error, not a finding", func(t *testing.T) {
		k := goodKMS()
		k.err = errors.New("dial tcp: connection refused")
		if _, err := CheckSSMContract(t.Context(), newFakeSSM(), k, testCMK, testPath); err == nil {
			t.Error("want an error")
		}
	})
}

// TestSSMContractRefusesFailedPutOrGet: each of the probe's three calls
// failing is a refusal naming the call.
func TestSSMContractRefusesFailedPutOrGet(t *testing.T) {
	fixedProbeName(t)
	denied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform ssm:X"}
	for _, tc := range []struct {
		name      string
		setup     func(*fakeSSM)
		wantFound []string
		leftOver  bool
	}{
		{name: "put denied", setup: func(s *fakeSSM) { s.putErr = denied }, wantFound: []string{"ssm:PutParameter", "AccessDeniedException", testCMK}},
		{name: "key refused by SSM", setup: func(s *fakeSSM) { s.putErr = &smithy.GenericAPIError{Code: "InvalidKeyId"} }, wantFound: []string{"ssm:PutParameter", "InvalidKeyId"}},
		{name: "get denied", setup: func(s *fakeSSM) { s.getErr = denied }, wantFound: []string{"ssm:GetParameters", "AccessDeniedException"}},
		{name: "value not decrypted", setup: func(s *fakeSSM) { s.noDecrypt = true }, wantFound: []string{"not decrypted"}},
		{name: "delete denied", setup: func(s *fakeSSM) { s.delErr = denied }, wantFound: []string{"ssm:DeleteParameter", "still there", SSMProbeNamePrefix}, leftOver: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeSSM()
			tc.setup(s)
			findings, err := CheckSSMContract(t.Context(), s, goodKMS(), testCMK, testPath)
			if err != nil {
				t.Fatal(err)
			}
			if k := ssmFindingFor(t, findings, SSMCustomerManagedKey); !k.OK() {
				t.Errorf("the key finding failed too: %s", k.Found)
			}
			f := ssmFindingFor(t, findings, SSMParameterAccess)
			if f.OK() || f.Unwaivable {
				t.Fatalf("finding = %+v, want a waivable failure", f)
			}
			for _, want := range tc.wantFound {
				if !strings.Contains(f.Found, want) {
					t.Errorf("Found = %q, want it to contain %q", f.Found, want)
				}
			}
			if left := len(s.params) > 0; left != tc.leftOver {
				t.Errorf("probe left behind = %v, want %v: %v", left, tc.leftOver, s.params)
			}

			s2 := newFakeSSM()
			tc.setup(s2)
			_, err = AssertSSMContract(t.Context(), s2, goodKMS(), testCMK, testPath, nil)
			var refusal *SSMContractRefusalError
			if !errors.As(err, &refusal) || len(refusal.Findings) != 1 || refusal.Findings[0].Setting != SSMParameterAccess {
				t.Fatalf("AssertSSMContract = %v, want a refusal of parameter_access alone", err)
			}
			if !strings.Contains(err.Error(), "ssm:GetParametersByPath") {
				t.Errorf("refusal does not list the permissions the role needs: %s", err)
			}
		})
	}
}

// TestSSMContractWaiverReachesExactlyWhatItNames: allow_insecure in the ssm
// block lets a run past the one setting it names and refuses on the other.
func TestSSMContractWaiverReachesExactlyWhatItNames(t *testing.T) {
	fixedProbeName(t)
	denied := &smithy.GenericAPIError{Code: "AccessDeniedException"}

	bothFail := func() (*fakeSSM, *fakeKMS) {
		s := newFakeSSM()
		s.putErr = denied
		k := goodKMS()
		k.err = denied // DescribeKey denied: Unreadable
		return s, k
	}

	for _, tc := range []struct {
		waived      []string
		wantRefused []Setting
		wantWaived  []Setting
	}{
		{waived: nil, wantRefused: []Setting{SSMCustomerManagedKey, SSMParameterAccess}},
		{waived: []string{"customer_managed_key"}, wantRefused: []Setting{SSMParameterAccess}, wantWaived: []Setting{SSMCustomerManagedKey}},
		{waived: []string{"parameter_access"}, wantRefused: []Setting{SSMCustomerManagedKey}, wantWaived: []Setting{SSMParameterAccess}},
		{waived: []string{"customer_managed_key", "parameter_access"}, wantWaived: []Setting{SSMCustomerManagedKey, SSMParameterAccess}},
		// A bucket waiver names nothing here.
		{waived: []string{"versioning"}, wantRefused: []Setting{SSMCustomerManagedKey, SSMParameterAccess}},
	} {
		t.Run(strings.Join(tc.waived, ","), func(t *testing.T) {
			s, k := bothFail()
			waived, err := AssertSSMContract(t.Context(), s, k, testCMK, testPath, tc.waived)
			var got []Setting
			var refusal *SSMContractRefusalError
			if errors.As(err, &refusal) {
				for _, f := range refusal.Findings {
					got = append(got, f.Setting)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !equalSettings(got, tc.wantRefused) {
				t.Errorf("refused %v, want %v", got, tc.wantRefused)
			}
			if len(tc.wantRefused) == 0 {
				var gotWaived []Setting
				for _, f := range waived {
					gotWaived = append(gotWaived, f.Setting)
				}
				if !equalSettings(gotWaived, tc.wantWaived) {
					t.Errorf("waived %v, want %v", gotWaived, tc.wantWaived)
				}
			}
		})
	}

	for _, s := range SSMSettings {
		if cost := SSMWaiverCost(s); cost == "" || cost == "that assertion is not made" {
			t.Errorf("SSMWaiverCost(%q) = %q, want the setting's own cost", s, cost)
		}
	}
}

func equalSettings(a, b []Setting) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
