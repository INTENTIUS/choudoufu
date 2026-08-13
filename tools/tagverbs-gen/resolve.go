// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "strings"

// normalizeServiceID folds a CFN service segment or a botocore directory
// name down to a bare lowercase alphanumeric run, so "ApplicationAutoScaling"
// (the CFN segment) and "application-autoscaling" (the botocore directory)
// compare equal despite the punctuation convention each side happens to use.
// The same trick tools/mapping-gen/namesdata.go's normalizeServiceID applies
// to the AWS SDK id / CFN service join, duplicated here for the same
// package-main reason every other tools/*-gen duplicates a small pure
// function rather than reaching into a sibling main package.
func normalizeServiceID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		}
	}
	return b.String()
}

// manualServiceAliases is the short, explicitly-justified list of CFN
// service segments whose botocore directory name does not fall out of
// normalizeServiceID at all - genuine AWS SDK abbreviations, not a growing
// hand table of judgment calls. Checked against the actual fetched
// directory listing at generation time (resolveDirectory below refuses an
// alias whose target no longer exists), so a stale entry is caught rather
// than silently wrong.
var manualServiceAliases = map[string]string{
	// CloudFormation's AWS::CertificateManager:: namespace; the AWS SDK
	// (and the AWS CLI command) is "acm".
	"CertificateManager": "acm",
	// CloudFormation's AWS::ElasticLoadBalancingV2:: namespace; the AWS SDK
	// directory and CLI command are "elbv2".
	"ElasticLoadBalancingV2": "elbv2",
	// CloudFormation's AWS::ElasticLoadBalancing:: namespace (the classic,
	// pre-ALB/NLB API); the AWS SDK directory and CLI command are "elb".
	"ElasticLoadBalancing": "elb",
}

// resolveDirectory joins a CFN service segment to the botocore directory
// that carries its service model: first an exact normalized match against
// the directories the pinned tree actually has, then manualServiceAliases -
// itself checked against the same directory set, so an alias whose target
// has vanished upstream is refused rather than trusted blindly. A segment
// neither path resolves reports ok=false; this function never guesses at a
// directory the tree does not actually contain.
func resolveDirectory(cfnService string, dirVersions map[string]string) (dir string, ok bool) {
	target := normalizeServiceID(cfnService)

	// Build the normalized index once per call is wasteful across ~190
	// services, but this tool runs once per generation and dirVersions is a
	// few hundred entries; the clarity of a self-contained function is
	// worth more here than the micro-optimization of hoisting it.
	var matches []string
	for d := range dirVersions {
		if normalizeServiceID(d) == target {
			matches = append(matches, d)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	if len(matches) > 1 {
		// Never observed against the pinned tag, but ambiguity is refused
		// rather than picked, the same discipline
		// tools/mapping-gen/namesdata.go's deriveServiceAliases applies to a
		// normalized-spelling collision.
		return "", false
	}

	if alias, found := manualServiceAliases[cfnService]; found {
		if _, exists := dirVersions[alias]; exists {
			return alias, true
		}
	}
	return "", false
}
