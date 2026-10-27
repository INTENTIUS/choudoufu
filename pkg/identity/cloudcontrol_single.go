// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

// Single-part Cloud Control identifiers that are not the Terraform id
// (INTENTIUS/plunge#28). cloudcontrol.go and cloudcontrol_hand.go translate the
// multi-part identifiers; a single-part one was taken as the id as it is. For
// most types it is: the type's one primary identifier property is the
// Terraform id (AWS::S3::Bucket's BucketName, aws_s3_bucket's bucket). The
// audit for plunge#28 compared every type plunge covers (pulumi-aws, whose id
// is the Terraform id) by its primary identifier (pkg/registry, from the
// CloudFormation schemas) against its import id row in this package, and found
// three other cases:
//
//   - the id is another property of the model: an ARN-identified type whose id
//     is a name or a server-assigned ID (AWS::VpcLattice::Service is listed by
//     Arn; aws_vpclattice_service's id is Id), or the reverse
//     (AWS::KinesisFirehose::DeliveryStream is listed by DeliveryStreamName;
//     its id is the ARN). ccSingleProps.
//   - the id is built from more than one property, or from the ARN
//     (ccSingleBuilds).
//   - the id needs a value neither the identifier nor the model carries, such
//     as the account ID or an ARN the model leaves out. The type is left
//     uncovered with that reason (ccSingleUncovered).
//
// Every other single-part identifier is the id as it is, provided it has the
// id's shape. An emulator does not always list by the schema's primary
// identifier: floci lists AWS::Lambda::Function by its ARN where the schema
// names FunctionName, and AWS::SQS::Queue by its name where the schema names
// QueueUrl. When the identifier's shape is not the primary identifier's (an
// ARN where a name belongs, a name where a URL belongs), the id is read from
// the model's primary identifier property, and a name or ID is cut from an ARN
// when the model has none. When even that fails the identifier is kept as it
// is, and the engine matches it against its own records (plunge's sweep looks
// it up among the records' ARN, URL and name outputs).

import (
	"fmt"
	"strings"
)

// ccSingleProps are the covered types whose id is a property of the Cloud Control model other than the
// type's primary identifier, by Terraform type: {Terraform type, CloudFormation type, model property}.
var ccSingleProps = func() map[string][2]string {
	rows := [][3]string{
		{"aws_accessanalyzer_analyzer", "AWS::AccessAnalyzer::Analyzer", "AnalyzerName"},
		{"aws_amplify_app", "AWS::Amplify::App", "AppId"},
		{"aws_appsync_api", "AWS::AppSync::Api", "ApiId"},
		{"aws_auditmanager_framework", "AWS::AuditManager::AssessmentFramework", "FrameworkId"},
		{"aws_backup_framework", "AWS::Backup::Framework", "FrameworkName"},
		{"aws_backup_report_plan", "AWS::Backup::ReportPlan", "ReportPlanName"},
		{"aws_batch_job_definition", "AWS::Batch::JobDefinition", "JobDefinitionArn"},
		{"aws_bedrock_inference_profile", "AWS::Bedrock::ApplicationInferenceProfile", "InferenceProfileId"},
		{"aws_bedrockagent_flow", "AWS::Bedrock::Flow", "Id"},
		{"aws_bedrockagent_prompt", "AWS::Bedrock::Prompt", "Id"},
		{"aws_bedrockagentcore_evaluator", "AWS::BedrockAgentCore::Evaluator", "EvaluatorId"},
		{"aws_bedrockagentcore_harness", "AWS::BedrockAgentCore::Harness", "HarnessId"},
		{"aws_bedrockagentcore_memory", "AWS::BedrockAgentCore::Memory", "MemoryId"},
		{
			"aws_bedrockagentcore_online_evaluation_config", "AWS::BedrockAgentCore::OnlineEvaluationConfig",
			"OnlineEvaluationConfigId",
		},
		{"aws_bedrockagentcore_policy_engine", "AWS::BedrockAgentCore::PolicyEngine", "PolicyEngineId"},
		{"aws_cloudfront_function", "AWS::CloudFront::Function", "Name"},
		{"aws_cloudwatch_alarm_mute_rule", "AWS::CloudWatch::AlarmMuteRule", "Name"},
		{"aws_cloudwatch_contributor_insight_rule", "AWS::CloudWatch::InsightRule", "RuleName"},
		{"aws_codebuild_fleet", "AWS::CodeBuild::Fleet", "Name"},
		{"aws_connect_instance", "AWS::Connect::Instance", "Id"},
		{"aws_dms_endpoint", "AWS::DMS::Endpoint", "EndpointIdentifier"},
		{"aws_dms_s3_endpoint", "AWS::DMS::Endpoint", "EndpointIdentifier"},
		{"aws_dx_connection", "AWS::DirectConnect::Connection", "ConnectionId"},
		{"aws_dx_gateway", "AWS::DirectConnect::DirectConnectGateway", "DirectConnectGatewayId"},
		{"aws_dx_lag", "AWS::DirectConnect::Lag", "LagId"},
		{"aws_dx_private_virtual_interface", "AWS::DirectConnect::PrivateVirtualInterface", "VirtualInterfaceId"},
		{"aws_dx_public_virtual_interface", "AWS::DirectConnect::PublicVirtualInterface", "VirtualInterfaceId"},
		{"aws_dx_transit_virtual_interface", "AWS::DirectConnect::TransitVirtualInterface", "VirtualInterfaceId"},
		{"aws_ecs_task_definition", "AWS::ECS::TaskDefinition", "Family"},
		{"aws_gamelift_game_server_group", "AWS::GameLift::GameServerGroup", "GameServerGroupName"},
		{"aws_glue_catalog", "AWS::Glue::Catalog", "Name"},
		{"aws_iot_policy", "AWS::IoT::Policy", "PolicyName"},
		{"aws_kinesis_firehose_delivery_stream", "AWS::KinesisFirehose::DeliveryStream", "Arn"},
		{"aws_m2_application", "AWS::M2::Application", "ApplicationId"},
		{"aws_m2_environment", "AWS::M2::Environment", "EnvironmentId"},
		{"aws_media_packagev2_channel_group", "AWS::MediaPackageV2::ChannelGroup", "ChannelGroupName"},
		{"aws_networkflowmonitor_monitor", "AWS::NetworkFlowMonitor::Monitor", "MonitorName"},
		{"aws_observabilityadmin_telemetry_pipeline", "AWS::ObservabilityAdmin::TelemetryPipelines", "Arn"},
		{"aws_odb_cloud_autonomous_vm_cluster", "AWS::ODB::CloudAutonomousVmCluster", "CloudAutonomousVmClusterId"},
		{"aws_odb_cloud_exadata_infrastructure", "AWS::ODB::CloudExadataInfrastructure", "CloudExadataInfrastructureId"},
		{"aws_odb_cloud_vm_cluster", "AWS::ODB::CloudVmCluster", "CloudVmClusterId"},
		{"aws_odb_network", "AWS::ODB::OdbNetwork", "OdbNetworkId"},
		{"aws_odb_network_peering_connection", "AWS::ODB::OdbPeeringConnection", "OdbPeeringConnectionId"},
		{"aws_osis_pipeline", "AWS::OSIS::Pipeline", "PipelineName"},
		{"aws_prometheus_scraper", "AWS::APS::Scraper", "ScraperId"},
		{"aws_prometheus_workspace", "AWS::APS::Workspace", "WorkspaceId"},
		{"aws_redshift_snapshot_schedule", "AWS::Redshift::SnapshotSchedule", "ScheduleIdentifier"},
		{"aws_s3files_file_system", "AWS::S3Files::FileSystem", "FileSystemId"},
		{"aws_sagemaker_algorithm", "AWS::SageMaker::Algorithm", "AlgorithmName"},
		{"aws_sagemaker_data_quality_job_definition", "AWS::SageMaker::DataQualityJobDefinition", "JobDefinitionName"},
		{"aws_sagemaker_endpoint", "AWS::SageMaker::Endpoint", "EndpointName"},
		{"aws_sagemaker_hub", "AWS::SageMaker::Hub", "HubName"},
		{"aws_sagemaker_image", "AWS::SageMaker::Image", "ImageName"},
		{"aws_sagemaker_model", "AWS::SageMaker::Model", "ModelName"},
		{"aws_sagemaker_model_package_group", "AWS::SageMaker::ModelPackageGroup", "ModelPackageGroupName"},
		{"aws_sagemaker_project", "AWS::SageMaker::Project", "ProjectName"},
		{"aws_schemas_discoverer", "AWS::EventSchemas::Discoverer", "DiscovererId"},
		{"aws_securityhub_connector_v2", "AWS::SecurityHub::ConnectorV2", "ConnectorId"},
		{"aws_shield_protection", "AWS::Shield::Protection", "ProtectionId"},
		{"aws_shield_protection_group", "AWS::Shield::ProtectionGroup", "ProtectionGroupId"},
		{"aws_transfer_server", "AWS::Transfer::Server", "ServerId"},
		{"aws_transfer_web_app", "AWS::Transfer::WebApp", "WebAppId"},
		{"aws_vpclattice_domain_verification", "AWS::VpcLattice::DomainVerification", "Id"},
		{"aws_vpclattice_resource_configuration", "AWS::VpcLattice::ResourceConfiguration", "Id"},
		{"aws_vpclattice_resource_gateway", "AWS::VpcLattice::ResourceGateway", "Id"},
		{"aws_vpclattice_service", "AWS::VpcLattice::Service", "Id"},
		{"aws_vpclattice_service_network", "AWS::VpcLattice::ServiceNetwork", "Id"},
		{
			"aws_vpclattice_service_network_resource_association", "AWS::VpcLattice::ServiceNetworkResourceAssociation",
			"Id",
		},
		{"aws_vpclattice_service_network_service_association", "AWS::VpcLattice::ServiceNetworkServiceAssociation", "Id"},
		{"aws_vpclattice_service_network_vpc_association", "AWS::VpcLattice::ServiceNetworkVpcAssociation", "Id"},
		{"aws_vpclattice_target_group", "AWS::VpcLattice::TargetGroup", "Id"},
		{"aws_workspaces_ip_group", "AWS::WorkSpaces::WorkspaceIpGroup", "GroupId"},
		{"aws_xray_sampling_rule", "AWS::XRay::SamplingRule", "RuleName"},
	}
	m := make(map[string][2]string, len(rows))
	for _, r := range rows {
		m[r[0]] = [2]string{r[1], r[2]}
	}
	return m
}()

// ccSingleBuild is a single-part translation written out from the Terraform provider's id for the type.
type ccSingleBuild struct {
	cfnType string
	// props are the model properties build reads; the listing reads the resource when the list handler left one out.
	props []string
	build func(identifier string, props map[string]any) (string, bool)
}

// ccSingleBuilds are the single-part translations that build the id, by Terraform type.
var ccSingleBuilds = map[string]ccSingleBuild{
	// aws_cloudwatch_event_rule's id is the rule name on the default event bus and "<bus>/<name>" on any other
	// (ruleCreateResourceID). The identifier is the rule's ARN, whose resource is rule/<name> or rule/<bus>/<name>.
	"aws_cloudwatch_event_rule": {cfnType: "AWS::Events::Rule", build: func(id string, _ map[string]any) (string, bool) {
		res, ok := arnResource(id)
		if !ok || !strings.HasPrefix(res, "rule/") {
			return "", false
		}
		rest := strings.TrimPrefix(res, "rule/")
		if bus, name, has := strings.Cut(rest, "/"); has {
			if bus == "default" {
				return name, name != ""
			}
			return rest, bus != "" && name != ""
		}
		return rest, rest != ""
	}},
	// aws_db_proxy_endpoint's id is "<proxy name>/<endpoint name>"; the identifier is the endpoint name.
	"aws_db_proxy_endpoint": {cfnType: "AWS::RDS::DBProxyEndpoint", props: []string{"DBProxyName"},
		build: func(id string, props map[string]any) (string, bool) {
			proxy, _ := props["DBProxyName"].(string)
			return proxy + "/" + id, proxy != "" && id != ""
		}},
	// aws_bedrock_guardrail's id is "<guardrail id>,<version>"; the identifier is the guardrail's ARN.
	"aws_bedrock_guardrail": {cfnType: "AWS::Bedrock::Guardrail", props: []string{"GuardrailId", "Version"},
		build: func(_ string, props map[string]any) (string, bool) {
			gid, _ := props["GuardrailId"].(string)
			version, _ := props["Version"].(string)
			return gid + "," + version, gid != "" && version != ""
		}},
	// aws_securitylake_subscriber's id is the subscriber ID, the last segment of the subscriber ARN the identifier
	// is (arn:aws:securitylake:<region>:<account>:subscriber/<id>); the model carries no ID of its own.
	"aws_securitylake_subscriber": {cfnType: "AWS::SecurityLake::Subscriber",
		build: func(id string, _ map[string]any) (string, bool) {
			res, ok := arnResource(id)
			if !ok || !strings.HasPrefix(res, "subscriber/") {
				return "", false
			}
			sub := strings.TrimPrefix(res, "subscriber/")
			return sub, sub != "" && !strings.Contains(sub, "/")
		}},
}

// ccSingleUncovered are the covered types whose id needs a value the Cloud Control identifier and model
// do not carry, by Terraform type, with the reason.
var ccSingleUncovered = map[string]string{
	"aws_codepipeline_webhook": "aws_codepipeline_webhook's id is the webhook's ARN, which needs the account ID; " +
		"AWS::CodePipeline::Webhook's model carries only its name",
	"aws_ecs_capacity_provider": "aws_ecs_capacity_provider's id is the capacity provider's ARN, which needs the " +
		"account ID; AWS::ECS::CapacityProvider's model carries only its name",
	"aws_elastic_beanstalk_environment": "aws_elastic_beanstalk_environment's id is the environment ID (e-...), " +
		"which AWS::ElasticBeanstalk::Environment's model does not carry",
	"aws_kinesisanalyticsv2_application": "aws_kinesisanalyticsv2_application's id is the application's ARN, which " +
		"needs the account ID; AWS::KinesisAnalyticsV2::Application's model carries only its name",
	"aws_s3control_storage_lens_configuration": "aws_s3control_storage_lens_configuration's id starts with the " +
		"account ID (\"<account id>:<config id>\"), which Cloud Control does not carry",
}

// ccIDShape is what a Terraform id looks like, read from the primary identifier property's name.
type ccIDShape int

const (
	ccShapePlain ccIDShape = iota // a name or an ID
	ccShapeARN
	ccShapeURL
)

func primaryShape(property string) ccIDShape {
	l := strings.ToLower(property)
	switch {
	case strings.HasSuffix(l, "arn"):
		return ccShapeARN
	case strings.HasSuffix(l, "url"):
		return ccShapeURL
	}
	return ccShapePlain
}

func (s ccIDShape) fits(v string) bool {
	switch s {
	case ccShapeARN:
		return strings.HasPrefix(v, "arn:")
	case ccShapeURL:
		return strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")
	}
	return v != "" && !strings.HasPrefix(v, "arn:") && !strings.Contains(v, "://")
}

// ccSingle is how one single-part Cloud Control identifier of a type becomes the Terraform id.
type ccSingle struct {
	tfType string
	// primary is the type's primary identifier property, and shape the shape of the id it holds.
	primary string
	shape   ccIDShape
	// prop is the model property the id is, when it is not the primary identifier (ccSingleProps).
	prop string
	// build is a written-out translation (ccSingleBuilds).
	build *ccSingleBuild
}

// ccSingleTranslation is the translation of tfType's single-part Cloud Control identifiers, listed through cfnType
// whose primary identifier property is primary. The reason says why there is none.
func ccSingleTranslation(tfType, cfnType, primary string) (*CloudControlID, string) {
	if reason, ok := ccSingleUncovered[tfType]; ok {
		return nil, reason
	}
	s := &ccSingle{tfType: tfType, primary: primary, shape: primaryShape(primary)}
	if b, ok := ccSingleBuilds[tfType]; ok && b.cfnType == cfnType {
		s.build = &b
	} else if p, ok := ccSingleProps[tfType]; ok && p[0] == cfnType {
		s.prop = p[1]
	}
	return &CloudControlID{tfType: tfType, arity: 1, single: s}, ""
}

// models reports the model properties the translation reads, which the listing reads the resource for when the list
// handler left them out.
func (s *ccSingle) models() []string {
	switch {
	case s.build != nil:
		return s.build.props
	case s.prop != "":
		return []string{s.prop}
	}
	return nil
}

// translate is the Terraform id for one listed object. ok is false when the model lacks what the id is read from;
// the caller reads the resource and tries again. A primary identifier of the wrong shape never fails: it is
// repaired where it can be, and otherwise kept for the sweep's record lookup to match.
func (s *ccSingle) translate(identifier string, props map[string]any) (string, bool) {
	if identifier == "" {
		return "", false
	}
	switch {
	case s.build != nil:
		return s.build.build(identifier, props)
	case s.prop != "":
		v, _ := props[s.prop].(string)
		return v, v != ""
	}
	if s.shape.fits(identifier) {
		return identifier, true
	}
	if v, _ := props[s.primary].(string); s.shape.fits(v) {
		return v, true
	}
	if s.shape == ccShapePlain {
		if res, ok := arnResource(identifier); ok {
			// A name or ID cut from an ARN: arn:aws:lambda:<region>:<account>:function:<name>, and the
			// resource/<name> forms.
			name := res[strings.LastIndexAny(res, ":/")+1:]
			if name != "" {
				return name, true
			}
		}
	}
	return identifier, true
}

// arnResource is the resource part of an ARN: everything after the fifth colon.
func arnResource(s string) (string, bool) {
	if !strings.HasPrefix(s, "arn:") {
		return "", false
	}
	parts := strings.SplitN(s, ":", 6)
	if len(parts) != 6 || parts[5] == "" {
		return "", false
	}
	return parts[5], true
}

// wantsModel reports whether the listing should read the resource before translating identifier: the list handler
// left out a property the id is read from, or the identifier is not of the id's shape and the model lacks the
// primary identifier property that would repair it.
func (s *ccSingle) wantsModel(identifier string, props map[string]any) bool {
	for _, p := range s.models() {
		if _, has := props[p]; !has {
			return true
		}
	}
	if s.build != nil || s.prop != "" || s.shape.fits(identifier) {
		return false
	}
	_, has := props[s.primary]
	return !has
}

// describe names what the translation reads, for an error message.
func (s *ccSingle) describe() string {
	switch {
	case s.build != nil:
		return fmt.Sprintf("%s's id, built from %s", s.tfType, strings.Join(append([]string{"the identifier"},
			s.build.props...), ", "))
	case s.prop != "":
		return fmt.Sprintf("%s's id, the model's %s", s.tfType, s.prop)
	}
	return s.tfType + "'s id"
}
