// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "fmt"

// trustPrincipals is the small pool of assume-role trust principals every
// team role and every ECS execution role draws from. A pool this small
// relative to the team count is what makes most role bodies literal
// duplicates of at least one other role once the resource's own name is
// stripped away - the "differing by a name prefix" half of #564's
// duplication requirement. Real estates reuse the same 2-4 trust
// boilerplates across most of their roles; this mirrors that.
var trustPrincipals = []string{
	"ec2.amazonaws.com",
	"lambda.amazonaws.com",
	"ecs-tasks.amazonaws.com",
}

// assumeRolePolicyHCL renders the jsonencode(...) expression for an
// assume-role trust policy naming a single service principal - the
// smallest, most common trust shape, and identical in body across every
// role that shares a principal.
func assumeRolePolicyHCL(principal string) string {
	return fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = %q }
    }]
  })`, principal)
}

// assumeRolePolicyCrossAccountHCL is a "scoped team"'s role trust: a
// cross-account assumption naming that team's own synthetic account ID
// (crossAccountID) rather than one of the small, shared service
// principals. This is the role side of the same boilerplate/scoped split
// templates.go's boilerplatePolicyHCL/scopedPolicyHCL apply to the
// customer-managed policy - a genuinely common, genuinely distinct IAM
// trust shape (an external account, not a service), so that not every
// role in the estate collapses into the same small duplicate pool.
func assumeRolePolicyCrossAccountHCL(accountID string) string {
	return fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { AWS = "arn:aws:iam::%s:root" }
    }]
  })`, accountID)
}

// crossAccountID derives a syntactically valid, 12-digit fake AWS account
// ID from a team index, deterministic and distinct per team.
func crossAccountID(i int) string {
	return fmt.Sprintf("%012d", 100000000000+i)
}

// inlineTemplates is the small pool of inline role-policy bodies team
// roles draw from, cycled independently of trustPrincipals. Every
// argument is name-independent (Resource is always "*"), so two inline
// policies drawing the same template are byte-identical except for the
// role they are scoped to - the required foreign-key argument an inline
// policy can never share with another role's.
var inlineTemplates = []struct {
	label   string
	actions []string
}{
	{"s3-readonly", []string{"s3:GetObject", "s3:ListBucket"}},
	{"logs-write", []string{"logs:CreateLogStream", "logs:PutLogEvents"}},
	{"dynamo-crud", []string{"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem"}},
}

func inlinePolicyHCL(actions []string) string {
	return fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = %s
      Resource = "*"
    }]
  })`, hclStringList(actions))
}

// boilerplatePolicies is the pool a "template team" (see gen.go's
// isBoilerplateTeam) draws its customer-managed policy from: generic
// actions against a shared, non-team-specific resource. Two teams
// assigned the same boilerplate index get a byte-identical policy body
// once each block's own name is stripped - true content duplication, not
// merely a shared shape, matching #564's "near-identical ... policies"
// half literally rather than loosely.
var boilerplatePolicies = []struct {
	label   string
	actions []string
}{
	{"s3-full-shared", []string{"s3:*"}},
	{"read-audit-shared", []string{"cloudtrail:LookupEvents", "config:Get*", "config:Describe*"}},
}

func boilerplatePolicyHCL(actions []string) string {
	return fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = %s
      Resource = "arn:aws:s3:::terralith-shared-bucket/*"
    }]
  })`, hclStringList(actions))
}

// scopedPolicyHCL is a "unique team"'s customer-managed policy: it embeds
// the team's own name in the resource ARN, so its body is genuinely
// distinct from every other team's policy rather than a name-substituted
// copy - the deliberate other half of the identity layer, standing in for
// the genuinely per-team configuration a real estate also accumulates
// alongside its copy-paste boilerplate.
func scopedPolicyHCL(teamName string, actions []string) string {
	return fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = %s
      Resource = "arn:aws:s3:::%s-data/*"
    }]
  })`, hclStringList(actions), teamName)
}

// managedPolicyARNs is the small pool of AWS-managed policy ARNs team
// attachments and (separately) ECS execution-role attachments draw from.
var managedPolicyARNs = []string{
	"arn:aws:iam::aws:policy/ReadOnlyAccess",
	"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
	"arn:aws:iam::aws:policy/AmazonEC2ReadOnlyAccess",
}

const ecsExecutionPolicyARN = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"

// hclStringList renders a Go string slice as an HCL list-of-strings
// literal on one line, e.g. `["a", "b"]`.
func hclStringList(items []string) string {
	out := "["
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", it)
	}
	return out + "]"
}
