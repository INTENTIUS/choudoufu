// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// serviceProbe names, for one floci /_localstack/health service id, an aws
// CLI top-level command plus a ranked list of candidate subcommands to try
// against it - each one a real operation with zero REQUIRED input members
// (so calling it with no arguments beyond --endpoint-url/--region is never
// a malformed request), preferring List*/Describe*/Get* operations whose
// name is short, which in practice surfaces the well-known central
// operation for a service (ec2's describe-vpcs, not one of its three
// recycle-bin List* operations) ahead of long-tail ones a partial emulator
// is far less likely to have wired up. probeOneService (health.go) tries
// them in order and stops at the first one that reaches a real handler -
// see that file's doc comment for what "reaches a real handler" means and
// why a single candidate is not trustworthy on its own.
type serviceProbe struct {
	cliCommand string
	candidates []string
}

// serviceProbes is derived, not hand-typed: built once (2026-08-18) by
// walking every service model shipped in a local botocore install
// (site-packages/botocore/data, one JSON file per AWS service) for each of
// the 82 service ids issue #276 measured live at
// http://.../_localstack/health against the pinned image
// (ghcr.io/lex00/floci@sha256:c3cdb09a...), picking every List*/Describe*/
// Get* operation whose input shape has no required members, sorting by
// name length, and keeping the shortest 8. Three services needed a
// hand-verified override for the aws CLI's own top-level command name,
// which is not always the botocore data-directory name (confirmed against
// `aws help`'s 434-entry AVAILABLE SERVICES list): s3's raw API sits under
// "s3api" ("s3" is the high-level file-transfer wrapper with no
// list-buckets subcommand of its own), AWS Config's CLI command is
// "configservice", and CodeDeploy's is "deploy". A handful of floci health
// keys also don't match their botocore directory name at all
// (elasticloadbalancing/elasticmapreduce/email/iotdata/monitoring/states/
// tagging) - resolved the same way, by reading each candidate service
// model's own metadata.endpointPrefix rather than guessing.
//
// Every entry below was then live-verified in the same session against a
// throwaway container of the pinned image: 68 of these 77 services reached
// a real handler on the first or a later candidate (recorded evidence in
// live/floci-capabilities.json cites which), 9 exhausted every candidate as
// a genuine router refusal (UnsupportedOperation/UnknownOperationException
// from floci itself, in two cases - sagemaker - with floci's own error text
// literally saying "is not supported by floci"). No entry here is a guess.
//
// Five services present in the health response as of that same measurement
// have NO zero-required-param List/Describe/Get operation in their own
// botocore model at all (appconfigdata, application-autoscaling,
// cognito-idp, rds-data, swf) - they carry no entry here and
// probeOneService records them "unverified" rather than fabricating a
// call. A future floci service not covered here (new to a later health
// response) gets the same honest "unverified", never a guessed candidate.
//
// This table only ever needs regenerating if floci starts naming services
// this checkout has not seen, or if a service's own API drops every
// candidate below (both would show up as a sudden run of "unverified"
// rows against a digest this table was supposed to cover) - not on every
// digest bump, since the table is about botocore's service models, not
// about any one floci image.
var serviceProbes = map[string]serviceProbe{
	"acm":                  {cliCommand: "acm", candidates: []string{"list-certificates", "get-account-configuration"}},
	"amp":                  {cliCommand: "amp", candidates: []string{"list-scrapers", "list-workspaces", "get-default-scraper-configuration"}},
	"apigateway":           {cliCommand: "apigateway", candidates: []string{"get-account", "get-api-keys", "get-rest-apis", "get-sdk-types", "get-vpc-links", "get-usage-plans", "get-domain-names", "get-client-certificates"}},
	"apigatewayv2":         {cliCommand: "apigatewayv2", candidates: []string{"get-apis", "get-vpc-links", "list-portals", "get-domain-names", "list-portal-products"}},
	"appconfig":            {cliCommand: "appconfig", candidates: []string{"list-extensions", "list-applications", "get-account-settings", "list-deployment-strategies", "list-extension-associations"}},
	"appsync":              {cliCommand: "appsync", candidates: []string{"list-apis", "list-domain-names", "list-graphql-apis"}},
	"athena":               {cliCommand: "athena", candidates: []string{"list-work-groups", "list-data-catalogs", "list-named-queries", "list-engine-versions", "list-query-executions", "list-application-dpu-sizes", "list-capacity-reservations"}},
	"autoscaling":          {cliCommand: "autoscaling", candidates: []string{"describe-tags", "describe-policies", "describe-account-limits", "describe-adjustment-types", "describe-scheduled-actions", "describe-auto-scaling-groups", "describe-scaling-activities", "describe-lifecycle-hook-types"}},
	"backup":               {cliCommand: "backup", candidates: []string{"list-copy-jobs", "list-scan-jobs", "list-backup-jobs", "list-frameworks", "list-legal-holds", "list-report-jobs", "list-backup-plans", "list-report-plans"}},
	"batch":                {cliCommand: "batch", candidates: []string{"list-jobs", "list-service-jobs", "describe-job-queues", "describe-job-definitions", "list-scheduling-policies", "list-consumable-resources", "describe-compute-environments", "describe-service-environments"}},
	"bcm-data-exports":     {cliCommand: "bcm-data-exports", candidates: []string{"list-tables", "list-exports"}},
	"bedrock-runtime":      {cliCommand: "bedrock-runtime", candidates: []string{"list-async-invokes"}},
	"ce":                   {cliCommand: "ce", candidates: []string{"get-anomaly-monitors", "list-cost-allocation-tags", "get-anomaly-subscriptions", "list-cost-category-definitions", "list-commitment-purchase-analyses", "list-cost-allocation-tag-backfill-history", "list-cost-category-resource-associations", "list-savings-plans-purchase-recommendation-generation"}},
	"cloudcontrol":         {cliCommand: "cloudcontrol", candidates: []string{"list-resource-requests"}},
	"cloudformation":       {cliCommand: "cloudformation", candidates: []string{"list-types", "list-stacks", "get-template", "list-exports", "describe-type", "get-hook-result", "list-stack-sets", "describe-events"}},
	"cloudfront":           {cliCommand: "cloudfront", candidates: []string{"list-functions", "list-key-groups", "list-public-keys", "list-vpc-origins", "list-trust-stores", "list-cache-policies", "list-distributions", "list-anycast-ip-lists"}},
	"cloudtrail":           {cliCommand: "cloudtrail", candidates: []string{"list-trails", "list-imports", "list-channels", "describe-query", "describe-trails", "list-dashboards", "list-public-keys", "get-insight-selectors"}},
	"codebuild":            {cliCommand: "codebuild", candidates: []string{"list-builds", "list-fleets", "list-reports", "list-projects", "list-sandboxes", "list-build-batches", "list-report-groups", "list-shared-projects"}},
	"codedeploy":           {cliCommand: "deploy", candidates: []string{"list-deployments", "list-applications", "list-deployment-configs", "list-on-premises-instances", "list-git-hub-account-token-names"}},
	"codepipeline":         {cliCommand: "codepipeline", candidates: []string{"list-webhooks", "list-pipelines", "list-rule-types", "list-action-types"}},
	"config":               {cliCommand: "configservice", candidates: []string{"list-stored-queries", "describe-config-rules", "get-custom-rule-policy", "list-resource-evaluations", "describe-conformance-packs", "describe-delivery-channels", "list-configuration-recorders", "get-discovered-resource-counts"}},
	"cur":                  {cliCommand: "cur", candidates: []string{"describe-report-definitions"}},
	"docdb":                {cliCommand: "docdb", candidates: []string{"describe-events", "describe-db-clusters", "describe-db-instances", "describe-certificates", "describe-db-subnet-groups", "describe-global-clusters", "describe-event-categories", "describe-db-engine-versions"}},
	"dynamodb":             {cliCommand: "dynamodb", candidates: []string{"list-tables", "list-backups", "list-exports", "list-imports", "describe-limits", "list-global-tables", "describe-endpoints", "list-contributor-insights"}},
	"ec2":                  {cliCommand: "ec2", candidates: []string{"describe-tags", "describe-vpcs", "describe-hosts", "describe-ipams", "describe-fleets", "describe-images", "describe-regions", "describe-subnets"}},
	"ecr":                  {cliCommand: "ecr", candidates: []string{"describe-registry", "get-registry-policy", "describe-repositories", "get-authorization-token", "get-signing-configuration", "list-pull-time-update-exclusions", "describe-pull-through-cache-rules", "get-registry-scanning-configuration"}},
	"ecs":                  {cliCommand: "ecs", candidates: []string{"list-tasks", "list-clusters", "list-services", "describe-clusters", "list-account-settings", "list-task-definitions", "list-container-instances", "describe-capacity-providers"}},
	"eks":                  {cliCommand: "eks", candidates: []string{"list-clusters", "list-access-policies", "describe-addon-versions", "describe-cluster-versions", "list-eks-anywhere-subscriptions"}},
	"elasticache":          {cliCommand: "elasticache", candidates: []string{"describe-users", "describe-events", "describe-snapshots", "describe-user-groups", "describe-cache-clusters", "describe-update-actions", "describe-service-updates", "describe-serverless-caches"}},
	"elasticbeanstalk":     {cliCommand: "elasticbeanstalk", candidates: []string{"describe-events", "describe-applications", "describe-environments", "list-platform-branches", "list-platform-versions", "describe-instances-health", "describe-platform-version", "describe-account-attributes"}},
	"elasticloadbalancing": {cliCommand: "elbv2", candidates: []string{"describe-rules", "describe-listeners", "describe-ssl-policies", "describe-trust-stores", "describe-target-groups", "describe-account-limits", "describe-load-balancers"}},
	"elasticmapreduce":     {cliCommand: "emr", candidates: []string{"list-studios", "list-clusters", "list-release-labels", "describe-release-label", "list-notebook-executions", "list-studio-session-mappings", "list-security-configurations", "get-block-public-access-configuration"}},
	"email":                {cliCommand: "ses", candidates: []string{"get-send-quota", "list-templates", "list-identities", "get-send-statistics", "list-receipt-filters", "list-receipt-rule-sets", "list-configuration-sets", "get-account-sending-enabled"}},
	"es":                   {cliCommand: "es", candidates: []string{"list-domain-names", "describe-packages", "list-vpc-endpoints", "list-elasticsearch-versions", "get-compatible-elasticsearch-versions", "describe-reserved-elasticsearch-instances", "describe-inbound-cross-cluster-search-connections", "describe-outbound-cross-cluster-search-connections"}},
	"events":               {cliCommand: "events", candidates: []string{"list-rules", "list-replays", "list-archives", "list-endpoints", "list-event-buses", "list-connections", "describe-event-bus", "list-event-sources"}},
	"firehose":             {cliCommand: "firehose", candidates: []string{"list-delivery-streams"}},
	"glue":                 {cliCommand: "glue", candidates: []string{"get-jobs", "list-jobs", "get-catalogs", "get-crawlers", "get-triggers", "list-schemas", "get-databases", "list-crawlers"}},
	"iam":                  {cliCommand: "iam", candidates: []string{"get-user", "list-roles", "list-users", "list-groups", "list-policies", "list-access-keys", "list-mfa-devices", "get-login-profile"}},
	"iot":                  {cliCommand: "iot", candidates: []string{"list-jobs", "list-things", "list-indices", "list-streams", "list-commands", "list-packages", "list-policies", "list-dimensions"}},
	"iotdata":              {cliCommand: "iot-data", candidates: []string{"list-retained-messages"}},
	"ivs":                  {cliCommand: "ivs", candidates: []string{"list-streams", "list-channels", "list-playback-key-pairs", "list-recording-configurations", "list-playback-restriction-policies"}},
	"ivschat":              {cliCommand: "ivschat", candidates: []string{"list-rooms", "list-logging-configurations"}},
	"kafka":                {cliCommand: "kafka", candidates: []string{"list-clusters", "list-clusters-v2", "list-replicators", "list-kafka-versions", "list-configurations", "list-vpc-connections", "get-compatible-kafka-versions"}},
	"kinesis":              {cliCommand: "kinesis", candidates: []string{"list-shards", "list-streams", "describe-limits", "describe-stream", "list-tags-for-stream", "describe-stream-summary", "describe-stream-consumer", "describe-account-settings"}},
	"kinesisanalytics":     {cliCommand: "kinesisanalytics", candidates: []string{"list-applications"}},
	"kms":                  {cliCommand: "kms", candidates: []string{"list-keys", "list-aliases", "describe-custom-key-stores"}},
	"lambda":               {cliCommand: "lambda", candidates: []string{"list-layers", "list-functions", "get-account-settings", "list-capacity-providers", "list-code-signing-configs", "list-event-source-mappings"}},
	"lightsail":            {cliCommand: "lightsail", candidates: []string{"get-disks", "get-alarms", "get-buckets", "get-bundles", "get-domains", "get-regions", "get-key-pairs", "get-instances"}},
	"logs":                 {cliCommand: "logs", candidates: []string{"list-anomalies", "list-log-groups", "describe-queries", "list-integrations", "describe-log-groups", "get-log-group-fields", "describe-deliveries", "describe-log-streams"}},
	"medialive":            {cliCommand: "medialive", candidates: []string{"list-inputs", "list-channels", "list-clusters", "list-networks", "list-versions", "list-offerings", "list-sdi-sources", "list-signal-maps"}},
	"mediapackage":         {cliCommand: "mediapackage", candidates: []string{"list-channels", "list-harvest-jobs", "list-origin-endpoints"}},
	"mediapackagev2":       {cliCommand: "mediapackagev2", candidates: []string{"list-channel-groups"}},
	"memorydb":             {cliCommand: "memorydb", candidates: []string{"describe-acls", "describe-users", "describe-events", "describe-clusters", "describe-snapshots", "describe-subnet-groups", "describe-reserved-nodes", "describe-engine-versions"}},
	"monitoring":           {cliCommand: "cloudwatch", candidates: []string{"list-metrics", "describe-alarms", "list-dashboards", "list-metric-streams", "describe-alarm-history", "describe-insight-rules", "describe-anomaly-detectors"}},
	"mq":                   {cliCommand: "mq", candidates: []string{"list-brokers", "list-configurations", "describe-broker-engine-types", "describe-broker-instance-options"}},
	"mwaa":                 {cliCommand: "mwaa", candidates: []string{"list-environments"}},
	"neptune":              {cliCommand: "neptune", candidates: []string{"describe-events", "describe-db-clusters", "describe-db-instances", "describe-db-subnet-groups", "describe-global-clusters", "describe-event-categories", "describe-db-engine-versions", "describe-db-parameter-groups"}},
	"pipes":                {cliCommand: "pipes", candidates: []string{"list-pipes"}},
	"pricing":              {cliCommand: "pricing", candidates: []string{"describe-services"}},
	"rds":                  {cliCommand: "rds", candidates: []string{"describe-events", "describe-db-proxies", "describe-db-clusters", "describe-db-instances", "describe-db-snapshots", "describe-export-tasks", "describe-certificates", "describe-integrations"}},
	"route53":              {cliCommand: "route53", candidates: []string{"get-geo-location", "list-hosted-zones", "list-geo-locations", "list-health-checks", "get-checker-ip-ranges", "get-hosted-zone-count", "get-health-check-count", "list-cidr-collections"}},
	"rum":                  {cliCommand: "rum", candidates: []string{"list-app-monitors"}},
	"s3":                   {cliCommand: "s3api", candidates: []string{"list-buckets", "list-directory-buckets"}},
	"s3vectors":            {cliCommand: "s3vectors", candidates: []string{"get-index", "list-indexes", "list-vectors", "get-vector-bucket", "list-vector-buckets", "get-vector-bucket-policy"}},
	"sagemaker":            {cliCommand: "sagemaker", candidates: []string{"list-apps", "list-hubs", "list-images", "list-models", "list-spaces", "list-trials", "list-actions", "list-devices"}},
	"scheduler":            {cliCommand: "scheduler", candidates: []string{"list-schedules", "list-schedule-groups"}},
	"secretsmanager":       {cliCommand: "secretsmanager", candidates: []string{"list-secrets", "get-random-password"}},
	"servicediscovery":     {cliCommand: "servicediscovery", candidates: []string{"list-services", "list-namespaces", "list-operations"}},
	"sns":                  {cliCommand: "sns", candidates: []string{"list-topics", "get-sms-attributes", "list-subscriptions", "list-origination-numbers", "list-phone-numbers-opted-out", "list-platform-applications", "get-sms-sandbox-account-status", "list-sms-sandbox-phone-numbers"}},
	"sqs":                  {cliCommand: "sqs", candidates: []string{"list-queues"}},
	"ssm":                  {cliCommand: "ssm", candidates: []string{"list-nodes", "get-inventory", "list-commands", "get-ops-summary", "list-documents", "list-ops-metadata", "describe-ops-items", "list-associations"}},
	"states":               {cliCommand: "stepfunctions", candidates: []string{"list-activities", "list-executions", "list-state-machines"}},
	"tagging":              {cliCommand: "resourcegroupstaggingapi", candidates: []string{"get-tag-keys", "get-resources", "list-required-tags", "get-compliance-summary", "describe-report-creation"}},
	"textract":             {cliCommand: "textract", candidates: []string{"list-adapters", "list-adapter-versions"}},
	"transcribe":           {cliCommand: "transcribe", candidates: []string{"list-vocabularies", "list-language-models", "list-call-analytics-jobs", "list-medical-scribe-jobs", "list-transcription-jobs", "list-vocabulary-filters", "list-medical-vocabularies", "list-call-analytics-categories"}},
	"transfer":             {cliCommand: "transfer", candidates: []string{"list-servers", "list-web-apps", "list-profiles", "list-workflows", "list-connectors", "list-certificates", "list-security-policies"}},
	"wafv2":                {cliCommand: "wafv2", candidates: []string{"get-web-acl", "get-rule-group"}},
}
