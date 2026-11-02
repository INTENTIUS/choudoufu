// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import "strings"

// ARN is one AWS ARN, split into the fields
// https://docs.aws.amazon.com/general/latest/gr/aws-arns-and-namespaces.html
// documents: arn:partition:service:region:account-id:resource.
type ARN struct {
	Partition string
	Service   string
	Region    string
	Account   string

	// ResourceType is the segment naming the resource's kind within its
	// service, when the ARN's resource field carries one: the "role" of
	// arn:aws:iam::111111111111:role/deploy, the "log-group" of
	// arn:aws:logs:us-east-1:111111111111:log-group:my-group. Empty when the
	// resource field is a bare identifier with no type segment at all - an
	// S3 bucket ARN (arn:aws:s3:::NAME) or an SNS topic ARN
	// (arn:aws:sns:REGION:ACCOUNT:NAME) carry only the name.
	ResourceType string

	// ResourceID is the resource's own identifier: the part of the resource
	// field after ResourceType's separator, or the whole resource field when
	// it carried no type segment. It may itself contain further "/" or ":"
	// characters - an ELBv2 load balancer's id is "app/NAME/HASH", an IAM
	// role with a path is "PATH/NAME" - because only the first separator in
	// the resource field divides type from id; nothing past it is split
	// again.
	ResourceID string
}

// ParseARN splits an AWS ARN into [ARN]'s fields, and its resource field
// further into a type and an id wherever the resource shows one.
//
// The Tagging API's GetResources (tagging.go's [Client.GetResources]) hands
// back real ARNs, and the AWS ARN reference is explicit that the resource
// field's own shape varies by service. Three shapes cover every service this
// fork's identity table admits:
//
//   - "type/id" - an IAM role (role/NAME), an EC2 VPC (vpc/vpc-ID), an ELBv2
//     target group (targetgroup/NAME/HASH, whose id itself carries a "/").
//   - "type:id" - a Lambda function (function:NAME), a CloudWatch Logs log
//     group (log-group:NAME), a Step Functions state machine
//     (stateMachine:NAME).
//   - a bare id, no type segment at all - an S3 bucket
//     (arn:aws:s3:::NAME) or an SNS topic (arn:aws:sns:REGION:ACCOUNT:NAME).
//
// Only the first "/" or ":" in the resource field - whichever comes first -
// divides type from id, and every admitted service uses at most one of the
// two separators to mean type/id at all, so a multi-segment id (the ELBv2
// case above) and a log group name that itself starts with "/" both stay
// inside ResourceID rather than being cut again.
//
// ok is false for anything that does not even have the six-colon-field
// arn:partition:service:region:account:resource shape with a literal "arn"
// first, or whose resource field is empty. Parsing never guesses at a shape
// a malformed string does not have.
func ParseARN(s string) (ARN, bool) {
	fields := strings.SplitN(s, ":", 6)
	if len(fields) != 6 || fields[0] != "arn" || fields[5] == "" {
		return ARN{}, false
	}

	a := ARN{
		Partition: fields[1],
		Service:   fields[2],
		Region:    fields[3],
		Account:   fields[4],
	}

	resource := fields[5]
	slash, colon := strings.IndexByte(resource, '/'), strings.IndexByte(resource, ':')
	switch {
	case slash >= 0 && (colon < 0 || slash < colon):
		a.ResourceType, a.ResourceID = resource[:slash], resource[slash+1:]
	case colon >= 0:
		a.ResourceType, a.ResourceID = resource[:colon], resource[colon+1:]
	default:
		a.ResourceID = resource
	}
	return a, true
}
