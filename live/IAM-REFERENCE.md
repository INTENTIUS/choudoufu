# IAM reference

Which AWS services let an IAM policy scope this fork's tagging action by
`aws:ResourceTag`, read from AWS's own Service Authorization Reference rather
than mapped by hand. A grant conditioned on `aws:ResourceTag/tofu-estate` is
known to bite only for the services in the first table.
[MARKERS.md](MARKERS.md) has the policies this feeds.

Rendered from `live/iam-reference.json` by `go run ./tools/iamref-gen -render`
(or `go run ./tools/gauntlet render`); `go run ./tools/iamref-gen` re-fetches
the reference. Edit the text around the generated markers, never inside them.

<!-- iamref-gen:begin iam-reference -->
From AWS's Service Authorization Reference (<https://servicereference.us-east-1.amazonaws.com/>), index last modified 2026-08-14. The admission table reaches 180 AWS services; 163 resolve to exactly one IAM name, and 157 IAM prefixes were checked for the condition key: 17 name `aws:ResourceTag` on at least one action, 140 name it on none.

### Services AWS names `aws:ResourceTag` for

| Service | IAM prefix | Actions naming it | Of total |
|---|---|---|---|
| EC2 | `ec2` | 495 | 793 |
| ResilienceHub | `resiliencehub` | 57 | 128 |
| SES | `ses` | 48 | 228 |
| AutoScaling | `autoscaling` | 42 | 68 |
| ECS | `ecs` | 37 | 81 |
| Kinesis | `kinesis` | 31 | 40 |
| CertificateManager | `acm` | 20 | 41 |
| CleanRooms | `cleanrooms` | 12 | 107 |
| ElastiCache | `elasticache` | 11 | 77 |
| CloudWatch | `cloudwatch` | 7 | 67 |
| SageMaker | `sagemaker` | 7 | 444 |
| WorkSpaces | `workspaces` | 7 | 101 |
| KafkaConnect | `kafkaconnect` | 2 | 18 |
| AuditManager | `auditmanager` | 1 | 62 |
| Batch | `batch` | 1 | 45 |
| CUR | `cur` | 1 | 12 |
| SSMQuickSetup | `ssm-quicksetup` | 1 | 14 |

### Services the reference says nothing about

Unmeasured, not disproven: the reference is authoritative about the keys it names and silent about the rest.

| Service | IAM prefix | Actions |
|---|---|---|
| Connect | `connect` | 378 |
| Glue | `glue` | 304 |
| IoT | `iot` | 293 |
| Bedrock | `bedrock` | 260 |
| BedrockAgentCore | `bedrock-agentcore` | 250 |
| DataZone | `datazone` | 247 |
| IAM | `iam` | 190 |
| S3 | `s3` | 180 |
| CloudFront | `cloudfront` | 173 |
| DocDB | `rds` | 169 |
| Redshift | `redshift` | 166 |
| SSM | `ssm` | 162 |
| Lightsail | `lightsail` | 161 |
| Logs | `logs` | 132 |
| SecurityHub | `securityhub` | 129 |
| SSO | `sso` | 126 |
| DMS | `dms` | 125 |
| MediaLive | `medialive` | 125 |
| Backup | `backup` | 122 |
| GameLift | `gamelift` | 120 |
| ServiceCatalog | `servicecatalog` | 117 |
| Lambda | `lambda` | 116 |
| SMSVOICE | `sms-voice` | 114 |
| Config | `config` | 102 |
| StorageGateway | `storagegateway` | 97 |
| NetworkManager | `networkmanager` | 95 |
| QBusiness | `qbusiness` | 93 |
| GuardDuty | `guardduty` | 91 |
| CodeCommit | `codecommit` | 90 |
| ODB | `odb` | 90 |
| AppStream | `appstream` | 89 |
| EMR | `elasticmapreduce` | 88 |
| AppSync | `appsync` | 87 |
| Comprehend | `comprehend` | 85 |
| NetworkFirewall | `network-firewall` | 84 |
| Athena | `athena` | 81 |
| DynamoDB | `dynamodb` | 79 |
| ElasticLoadBalancing | `elasticloadbalancing` | 77 |
| ImageBuilder | `imagebuilder` | 77 |
| IVS | `ivs` | 77 |
| Rekognition | `rekognition` | 76 |
| VpcLattice | `vpc-lattice` | 75 |
| WorkSpacesWeb | `workspaces-web` | 75 |
| Route53 | `route53` | 71 |
| Transfer | `transfer` | 71 |
| EKS | `eks` | 69 |
| Route53Resolver | `route53resolver` | 69 |
| RedshiftServerless | `redshift-serverless` | 68 |
| CodeBuild | `codebuild` | 67 |
| CloudTrail | `cloudtrail` | 66 |
| DataSync | `datasync` | 66 |
| Kendra | `kendra` | 66 |
| ControlTower | `controltower` | 65 |
| DirectConnect | `directconnect` | 64 |
| Location | `geo` | 64 |
| Organizations | `organizations` | 63 |
| WAFv2 | `wafv2` | 61 |
| ECR | `ecr` | 60 |
| Events | `events` | 60 |
| CE | `ce` | 59 |
| AppConfig | `appconfig` | 58 |
| FSx | `fsx` | 56 |
| GlobalAccelerator | `globalaccelerator` | 56 |
| KMS | `kms` | 56 |
| S3Tables | `s3tables` | 53 |
| CodeArtifact | `codeartifact` | 51 |
| ElasticBeanstalk | `elasticbeanstalk` | 50 |
| OpenSearchServerless | `aoss` | 49 |
| CodeDeploy | `codedeploy` | 48 |
| S3Outposts | `s3-outposts` | 48 |
| MemoryDB | `memorydb` | 47 |
| PaymentCryptography | `payment-cryptography` | 46 |
| ApiGateway | `apigateway` | 44 |
| CodePipeline | `codepipeline` | 44 |
| XRay | `xray` | 43 |
| AppRunner | `apprunner` | 42 |
| FMS | `fms` | 42 |
| Amplify | `amplify` | 41 |
| Notifications | `notifications` | 41 |
| SNS | `sns` | 41 |
| ObservabilityAdmin | `observabilityadmin` | 40 |
| SSMContacts | `ssm-contacts` | 40 |
| Chatbot | `chatbot` | 39 |
| Shield | `shield` | 39 |
| StepFunctions | `states` | 39 |
| CodeConnections | `codeconnections` | 38 |
| CodeStarConnections | `codestar-connections` | 38 |
| EFS | `elasticfilesystem` | 38 |
| Inspector | `inspector` | 37 |
| M2 | `m2` | 37 |
| ResourceExplorer2 | `resource-explorer-2` | 37 |
| Detective | `detective` | 36 |
| AppIntegrations | `app-integrations` | 35 |
| RAM | `ram` | 35 |
| KinesisAnalytics | `kinesisanalytics` | 34 |
| MediaPackageV2 | `mediapackagev2` | 34 |
| ServiceDiscovery | `servicediscovery` | 33 |
| Billing | `billing` | 32 |
| Route53RecoveryReadiness | `route53-recovery-readiness` | 32 |
| AppFlow | `appflow` | 31 |
| DevOpsGuru | `devops-guru` | 31 |
| SecurityLake | `securitylake` | 31 |
| SSMIncidents | `ssm-incidents` | 31 |
| VerifiedPermissions | `verifiedpermissions` | 31 |
| DSQL | `dsql` | 30 |
| RolesAnywhere | `rolesanywhere` | 30 |
| Cloud9 | `cloud9` | 29 |
| FIS | `fis` | 29 |
| ResourceGroups | `resource-groups` | 29 |
| Invoicing | `invoicing` | 26 |
| EMRContainers | `emr-containers` | 25 |
| EMRServerless | `emr-serverless` | 25 |
| Grafana | `grafana` | 25 |
| ARCRegionSwitch | `arc-region-switch` | 24 |
| S3Files | `s3files` | 24 |
| ACMPCA | `acm-pca` | 23 |
| SecretsManager | `secretsmanager` | 23 |
| Synthetics | `synthetics` | 22 |
| DataPipeline | `datapipeline` | 21 |
| RUM | `rum` | 20 |
| SQS | `sqs` | 20 |
| DocDBElastic | `docdb-elastic` | 19 |
| MediaPackage | `mediapackage` | 19 |
| S3Vectors | `s3vectors` | 19 |
| Route53Profiles | `route53profiles` | 18 |
| CodeGuruReviewer | `codeguru-reviewer` | 17 |
| InternetMonitor | `internetmonitor` | 17 |
| IVSChat | `ivschat` | 17 |
| ARCZonalShift | `arc-zonal-shift` | 15 |
| Oam | `oam` | 15 |
| ApplicationAutoScaling | `application-autoscaling` | 14 |
| Budgets | `budgets` | 13 |
| CodeStarNotifications | `codestar-notifications` | 13 |
| Scheduler | `scheduler` | 13 |
| MWAA | `airflow` | 12 |
| BCMDataExports | `bcm-data-exports` | 12 |
| Pipes | `pipes` | 10 |
| Rbin | `rbin` | 10 |
| NotificationsContacts | `notifications-contacts` | 9 |
| DLM | `dlm` | 8 |
<!-- iamref-gen:end iam-reference -->
