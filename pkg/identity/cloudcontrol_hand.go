// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

// The multi-part Cloud Control identifiers cloudcontrol.go's grammar rule
// cannot translate (INTENTIUS/plunge#19). Each of these taggable, listable
// types has a Terraform import id that this package's rows do not spell from
// the identifier's parts alone: it has no row, or its row is an ARN with the
// region and account in it, or it names the object by a part of an ARN. Each
// translation is written out from the Terraform provider's importer for the
// type.
//
// AWS::Budgets::BudgetsAction (aws_budgets_budget_action) has a Terraform id
// that starts with the account ID ("account_id:action_id:budget_name"), which
// neither the identifier nor the Cloud Control model carries. The engine
// supplies it through an [AccountSource] (plunge reads it with STS on the
// stack's own endpoint, INTENTIUS/plunge#31, and skips the listing with a
// warning when the stack names custom endpoints and none for STS).

import (
	"strings"
)

// ccHand is one written-out translation from a Cloud Control identifier (its parts, in primary identifier order) and,
// when prop is set, the resource model's prop, to the Terraform id.
type ccHand struct {
	tfType string
	// prop is a model property the id is read from (an ARN the list or read handler returns). Empty when the parts
	// are enough.
	prop  string
	arity int
	build func(parts []string, prop string) (string, bool)
	// account reports that build is given the account ID the provider runs as in place of prop.
	account bool
}

// ccHands are the written-out translations, by CloudFormation type.
var ccHands = map[string]ccHand{
	// aws_eip is imported by its allocation ID ("eipalloc-..."); the identifier is "PublicIp|AllocationId".
	"AWS::EC2::EIP": {tfType: "aws_eip", arity: 2, build: func(p []string, _ string) (string, bool) {
		return p[1], strings.HasPrefix(p[1], "eipalloc-")
	}},
	// aws_ecs_service is imported as "<cluster name>/<service name>"; the identifier is "ServiceArn|Cluster", both
	// ARNs (arn:aws:ecs:<region>:<account>:service/<cluster>/<name>, older services service/<name>).
	"AWS::ECS::Service": {tfType: "aws_ecs_service", arity: 2, build: func(p []string, _ string) (string, bool) {
		service := p[0][strings.LastIndex(p[0], "/")+1:]
		cluster := p[1]
		if i := strings.Index(cluster, ":cluster/"); i >= 0 {
			cluster = cluster[i+len(":cluster/"):]
		}
		if service == "" || cluster == "" || strings.Contains(cluster, "/") {
			return "", false
		}
		return cluster + "/" + service, true
	}},
	// The GuardDuty detector children are imported as "<detector id>:<child id>".
	"AWS::GuardDuty::IPSet": {tfType: "aws_guardduty_ipset", arity: 2, build: func(p []string, _ string) (string, bool) {
		return p[1] + ":" + p[0], true // Id|DetectorId
	}},
	"AWS::GuardDuty::ThreatIntelSet": {tfType: "aws_guardduty_threatintelset", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[1] + ":" + p[0], true }}, // Id|DetectorId
	"AWS::GuardDuty::PublishingDestination": {tfType: "aws_guardduty_publishing_destination", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[0] + ":" + p[1], true }}, // DetectorId|Id
	// aws_quicksight_folder is imported as "<account id>,<folder id>"; the identifier is "AwsAccountId|FolderId".
	"AWS::QuickSight::Folder": {tfType: "aws_quicksight_folder", arity: 2,
		build: func(p []string, _ string) (string, bool) { return p[0] + "," + p[1], true }},
	// aws_budgets_budget_action is imported as "<account id>:<action id>:<budget name>"; the identifier is
	// "ActionId|BudgetName", and the account ID comes from the engine's [AccountSource].
	"AWS::Budgets::BudgetsAction": {tfType: "aws_budgets_budget_action", arity: 2, account: true,
		build: func(p []string, account string) (string, bool) {
			return account + ":" + p[0] + ":" + p[1], account != "" && !strings.Contains(p[1], ":")
		}},
	// The SageMaker domain children are imported by their ARN, which pkg/identity spells with the region and the
	// account; the model carries the ARN itself.
	"AWS::SageMaker::App":   {tfType: "aws_sagemaker_app", prop: "AppArn", arity: 4, build: arnProp},
	"AWS::SageMaker::Space": {tfType: "aws_sagemaker_space", prop: "SpaceArn", arity: 2, build: arnProp},
	"AWS::SageMaker::UserProfile": {tfType: "aws_sagemaker_user_profile", prop: "UserProfileArn", arity: 2,
		build: arnProp},
}

func arnProp(_ []string, arn string) (string, bool) {
	return arn, strings.HasPrefix(arn, "arn:")
}

// ccHandTranslation is cfnType's written-out translation for tfType, when there is one.
func ccHandTranslation(tfType, cfnType string) (*CloudControlID, bool) {
	h, ok := ccHands[cfnType]
	if !ok || h.tfType != tfType {
		return nil, false
	}
	return &CloudControlID{tfType: tfType, arity: h.arity, hand: &h}, true
}
