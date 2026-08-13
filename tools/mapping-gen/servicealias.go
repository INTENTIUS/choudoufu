// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "strings"

// The service-alias heuristic (v2): a narrower, service-scoped second pass
// over whatever the overlay's Aliases/Folds/Nones and the name heuristic in
// heuristic.go leave at via:none.
//
// The name heuristic's one candidate per CFN type is service-then-resource
// concatenation (nameCandidates: "aws_" + service + "_" + resource). That
// trips over two shapes at once: a TF prefix that names no real AWS service
// (aws_vpc_* is EC2, not "VPC"), and a CFN resource segment that itself
// repeats the service word CloudFormation's own naming already redundantly
// carries (AWS::EC2::VPCPeeringConnection's resource segment starts with
// "VPC" again, so "ec2" + "vpc_peering_connection" never equals TF's own
// "vpc_peering_connection"). This pass fixes both: the overlay's
// ServiceAliases table says which CFN service(s) a TF prefix means, and the
// match against that service's resource names tries every point along the
// prefix where the boundary between "service word" and "resource word"
// could fall - not just the one the name heuristic assumes.
//
// The scoping is the safety property a cross-service join does not have
// (see mapping_gen_test.go's TestServiceAliasFalsePositiveGuard, and the
// two false hits - aws_amplify_webhook/CodePipeline::Webhook,
// aws_appsync_type/Cassandra::Type - that motivated this file's existence
// in the first place): every CFN type this pass can ever reach for a given
// TF type comes from a service the overlay explicitly named for that TF
// type's own prefix, never any other.

// serviceResourceIndex maps every resource-name spelling the service's CFN
// types produce - pascalToSnake's own output, and that same string with its
// underscores collapsed out - back to the CFN type(s) it came from. Scoped
// to one CFN service, so a candidate string two different CFN types in this
// same service both produce is a same-service collision (surfaced through
// serviceAliasCandidates' multi-hit return, never silently resolved); a
// candidate string some other, unrelated service's type would also produce
// is not reachable here at all, since that service's types never enter this
// index.
func serviceResourceIndex(cfnTypes []string, service string) map[string]map[string]bool {
	idx := map[string]map[string]bool{}
	for _, cfn := range cfnTypes {
		svc, resource, ok := splitCFNType(cfn)
		if !ok || svc != service {
			continue
		}
		snake := pascalToSnake(resource)
		add := func(candidate string) {
			if idx[candidate] == nil {
				idx[candidate] = map[string]bool{}
			}
			idx[candidate][cfn] = true
		}
		add(snake)
		if collapsed := strings.ReplaceAll(snake, "_", ""); collapsed != snake {
			add(collapsed)
		}
	}
	return idx
}

// serviceIndexCache memoizes serviceResourceIndex per CFN service name
// across a whole buildMapping run, since the same handful of aliased
// services (EC2, RDS, Logs, ...) is consulted once per unmapped TF type.
type serviceIndexCache map[string]map[string]map[string]bool

func (c serviceIndexCache) forService(cfnTypes []string, service string) map[string]map[string]bool {
	if idx, ok := c[service]; ok {
		return idx
	}
	idx := serviceResourceIndex(cfnTypes, service)
	c[service] = idx
	return idx
}

// tokenPrefixEqual reports whether tokens starts with prefixTokens,
// token-for-token - never a substring match, so a TF prefix like "s3" can
// never accidentally match a type whose own first token is "s3control".
func tokenPrefixEqual(tokens, prefixTokens []string) bool {
	if len(prefixTokens) > len(tokens) {
		return false
	}
	for i, p := range prefixTokens {
		if tokens[i] != p {
			return false
		}
	}
	return true
}

// serviceAliasCandidates returns every CFN type the service-alias heuristic
// reaches for tf: every service_aliases prefix that token-matches tf's own
// prefix contributes its CFN service(s); within each such service, every
// point along the matched prefix where a "strip the prefix's leading N
// tokens, keep the rest" cut could fall is tried, in both the exact
// pascalToSnake spelling and the underscore-collapsed one. A tf with no
// aws_ prefix, or one no service_aliases entry token-matches, yields no
// candidates at all - never falls through to any other service.
func serviceAliasCandidates(tf string, cfnTypes []string, serviceAliases map[string][]string, cache serviceIndexCache) map[string]bool {
	const awsPrefix = "aws_"
	if !strings.HasPrefix(tf, awsPrefix) {
		return nil
	}
	tokens := strings.Split(strings.TrimPrefix(tf, awsPrefix), "_")

	// No dedup by service across prefixes: two different service_aliases
	// keys naming the same CFN service (unusual, but not forbidden) can
	// still contribute different cut ranges, since each prefix's own
	// token length bounds how many leading tokens that prefix's cuts may
	// strip. serviceIndexCache already makes revisiting a service cheap,
	// so there is no dedup to lose by not short-circuiting here - and a
	// map-order-dependent short-circuit would make the result depend on
	// Go's randomized map iteration, which buildMapping cannot allow.
	hits := map[string]bool{}
	for prefix, services := range serviceAliases {
		ptoks := strings.Split(prefix, "_")
		if !tokenPrefixEqual(tokens, ptoks) {
			continue
		}
		for _, svc := range services {
			idx := cache.forService(cfnTypes, svc)
			for cut := 0; cut <= len(ptoks); cut++ {
				remainder := strings.Join(tokens[cut:], "_")
				if remainder == "" {
					continue
				}
				for cfnType := range idx[remainder] {
					hits[cfnType] = true
				}
				if collapsed := strings.ReplaceAll(remainder, "_", ""); collapsed != remainder {
					for cfnType := range idx[collapsed] {
						hits[cfnType] = true
					}
				}
			}
		}
	}
	return hits
}
