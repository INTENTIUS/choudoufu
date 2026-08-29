// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// This file is the one piece of knowledge that does not exist anywhere else
// in the tree: which .corpus directory (or directories) hold the real
// configuration each gauntlet estate's crossing script (live/e2e/<name>/
// run.sh) actually deploys in its cold_deploy stage. .corpus is gitignored
// (fetched by "just corpus-fetch"), and a crossing script computes its own
// working directory in shell variables (SRC, SRC_EXAMPLE, EST...) that
// nothing else reads, so issue #435's own finding - that a prior planning
// pass could not compute the board's exercised-type set at all - is exactly
// this gap.

// estateSpec is one gauntlet estate's static-analysis recipe: where its real
// resource declarations live, so [scanEstate] can point
// internal/live/check.Load (the same module-graph loader corpus-fetch and
// corpus-gen already use - see that package's load.go) at them instead of
// this tool inventing a second HCL walker.
type estateSpec struct {
	// Name must match a live/gauntlet.json entry exactly; TestEveryEstateHasTypes
	// holds this list to that artifact.
	Name string

	// ConfigDirs are repository-root-relative directories check.Load reads,
	// each as if it were its own root module. For most estates this is the
	// one example/deployment directory the crossing script copies verbatim
	// out of .corpus (module-relative "../.." and any registry module the
	// directory's own .terraform/modules resolves - installed there by
	// "just corpus-fetch" - come along for free through check.Load's module
	// walker). For an estate whose script copies several disjoint leaf
	// modules and wires them together itself (see ScanScript below), this
	// lists each copied leaf directory standalone.
	ConfigDirs []string

	// ScanScript, when true, adds a text scan of the crossing script itself
	// (live/e2e/<name>/run.sh) for literal `resource "type" "name"` blocks,
	// unioned with whatever ConfigDirs produced. It exists for the estates
	// whose script writes its own root wiring with a heredoc rather than
	// deploying a directory straight out of .corpus (the opentofu-native
	// lane's own convention - see each script's "write_root"/"write_main_tf"
	// function) - the heredoc itself is occasionally where a resource is
	// declared directly (reference-ec2-vpc's whole five-resource estate is
	// exactly this, with no .corpus source at all), and it is cheap
	// insurance everywhere else.
	//
	// The scan reads the script's ENTIRE text, including the day2_rename/
	// day2_remove/day2_replace oracle heredocs further down - a superset of
	// cold_deploy's own shape, which is fine here: this index answers "what
	// does this estate's crossing touch", not "what does stage 1 alone
	// apply".
	ScanScript bool

	// Note records why ConfigDirs is what it is, for the next person to
	// re-derive it rather than trust it - traced against the exact run.sh
	// lines cited.
	Note string
}

// estateSpecs is every estate in live/gauntlet.json, hand-traced against its
// own crossing script. TestEveryEstateHasTypes fails if this list and the
// artifact ever disagree with live/gauntlet.json's 26 rows.
var estateSpecs = []estateSpec{
	{
		Name:       "corpus-alb-complete",
		ConfigDirs: []string{".corpus/alb/examples/complete-alb"},
		Note:       `run.sh SRC="$CORPUS_DIR/alb"; copy_tree() copies the module root's own *.tf plus examples/complete-alb, preserving "../.." - identical layout to .corpus/alb itself, so check.Load resolves the module call and every registry dependency (acm, lambda x2, s3-bucket log_bucket, vpc) via that directory's own .terraform/modules, installed by "just corpus-fetch".`,
	},
	{
		Name:       "corpus-autoscaling-complete",
		ConfigDirs: []string{".corpus/autoscaling/examples/complete"},
		Note:       `run.sh SRC_EXAMPLE="$ROOT/.corpus/autoscaling/examples/complete".`,
	},
	{
		Name:       "corpus-dynamodb-table-basic",
		ConfigDirs: []string{".corpus/dynamodb-table/examples/basic"},
		Note:       `run.sh SRC_ROOT="$ROOT/.corpus/dynamodb-table", SRC_EXAMPLE="$SRC_ROOT/examples/basic".`,
	},
	{
		Name:       "corpus-ec2-instance-complete",
		ConfigDirs: []string{".corpus/ec2-instance/examples/complete"},
		Note:       `run.sh SRC_MODULE="$ROOT/.corpus/ec2-instance", copied whole via "cp -R \"$SRC_MODULE\"/."; the estate itself is examples/complete under it.`,
	},
	{
		Name:       "corpus-ecs-fargate",
		ConfigDirs: []string{".corpus/ecs/examples/fargate"},
		Note:       `run.sh SRC="$CORPUS_DIR/ecs"; copy_tree() mirrors corpus-alb-complete's shape for examples/fargate.`,
	},
	{
		Name:       "corpus-eks-basic",
		ConfigDirs: []string{".corpus/eks/examples/basic"},
		Note:       `run.sh SRC="$ROOT/.corpus/eks", copied whole ("cp -R \"$SRC\" \"$WORK/plain/eks\""); the estate is examples/basic, gated by "[ -d \"$SRC/examples/basic\" ]".`,
	},
	{
		Name:       "corpus-evoteum-modules",
		ConfigDirs: []string{".corpus/evoteum-tofu-modules/aws/networking", ".corpus/evoteum-tofu-modules/aws/dynamodb"},
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/evoteum-tofu-modules"; copy_modules() copies exactly "$SRC/aws/networking" and "$SRC/aws/dynamodb" (diff -rq verified byte-identical before any edit); write_root()'s own main.tofu heredoc wires them but declares no resources of its own beyond what those two modules hold.`,
	},
	{
		Name:       "corpus-giantswarm-crossplane",
		ConfigDirs: []string{".corpus/giantswarm-aws-prereqs/crossplane"},
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/giantswarm-aws-prereqs/crossplane"; copy_module() copies the whole (small) crossplane/ directory, including its policies/ subdirectory, verbatim.`,
	},
	{
		Name: "corpus-hongbomiao-harbor",
		ConfigDirs: []string{
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/amazon_s3_bucket",
			".corpus/hongbomiao/infrastructure/opentofu/modules/amazon-eks/harbor_iam_user",
		},
		ScanScript: true,
		Note:       `run.sh SRC_AWS=".../modules/aws", SRC_EKS=".../modules/amazon-eks"; copies exactly "$SRC_AWS/amazon_s3_bucket" and "$SRC_EKS/harbor_iam_user" - the one self-contained "Harbor" slice of kubernetes/main.tofu the script's own header documents choosing.`,
	},
	{
		Name: "corpus-hongbomiao-labelbox",
		ConfigDirs: []string{
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/amazon_s3_bucket",
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/amazon_s3_bucket_cors_configuration",
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/labelbox_iam_role",
		},
		ScanScript: true,
		Note:       `run.sh SRC=".../modules/aws"; copy_leaf_modules() loops "for m in amazon_s3_bucket amazon_s3_bucket_cors_configuration labelbox_iam_role".`,
	},
	{
		Name: "corpus-hongbomiao-storage",
		ConfigDirs: []string{
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/amazon_s3_bucket",
			".corpus/hongbomiao/infrastructure/opentofu/modules/aws/aws_kms_key",
		},
		ScanScript: true,
		Note:       `run.sh SRC=".../modules/aws"; copy_leaf_modules() loops "for m in amazon_s3_bucket aws_kms_key".`,
	},
	{
		Name:       "corpus-iam-policy",
		ConfigDirs: []string{".corpus/iam/examples/iam-policy"},
		Note:       `run.sh SRC_EXAMPLE="$ROOT/.corpus/iam/examples/iam-policy".`,
	},
	{
		Name:       "corpus-iam-read-only-policy",
		ConfigDirs: []string{".corpus/iam/examples/iam-read-only-policy"},
		Note:       `run.sh SRC_EXAMPLE="$CORPUS_DIR/iam/examples/iam-read-only-policy".`,
	},
	{
		Name:       "corpus-lambda-simple",
		ConfigDirs: []string{".corpus/lambda/examples/simple"},
		Note:       `run.sh SRC_EXAMPLE="$ROOT/.corpus/lambda/examples/simple" (SRC_FIXTURES holds only a zip fixture, no .tf).`,
	},
	{
		Name:       "corpus-leynos-monitoring",
		ConfigDirs: []string{".corpus/leynos-df12-www/modules/monitoring"},
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/leynos-df12-www/modules/monitoring"; copy_module() copies it whole under modules/monitoring.`,
	},
	{
		Name:       "corpus-mastino-dns",
		ConfigDirs: []string{".corpus/mastino/global/dns"},
		ScanScript: true,
		Note:       `run.sh SRC="$CORPUS_DIR/mastino/global/dns"; the only heredoc this script writes (terraform.tf) is backend/provider wiring, no resources.`,
	},
	{
		Name:       "corpus-overture-tiles",
		ConfigDirs: []string{".corpus/overture-tiles"},
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/overture-tiles"; copy_module()'s own comment: "the module's own top-level .tf files ... only its own root" - s3.tf, iam.tf, network.tf, batch.tf, cloudfront.tf (plus variables/outputs/versions, no resources) sit directly at that root. write_root() is thin module-call wiring.`,
	},
	{
		Name:       "corpus-rds-complete-postgres",
		ConfigDirs: []string{".corpus/rds/examples/complete-postgres"},
		Note:       `run.sh SRC="$CORPUS_DIR/rds"; copy_tree() mirrors corpus-alb-complete's shape for examples/complete-postgres.`,
	},
	{
		Name:       "corpus-s3-bucket-complete",
		ConfigDirs: []string{".corpus/s3-bucket/examples/complete"},
		Note:       `run.sh SRC="$CORPUS_DIR/s3-bucket"; copy_estate() copies root .tf files plus examples/complete/, preserving "../..".`,
	},
	{
		Name:       "corpus-security-group-complete",
		ConfigDirs: []string{".corpus/security-group/examples/complete"},
		Note:       `run.sh SRC="$CORPUS_DIR/security-group"; mirrors corpus-alb-complete's copy_tree shape for examples/complete.`,
	},
	{
		Name:       "corpus-simpleinfra-dns",
		ConfigDirs: []string{".corpus/simpleinfra/terraform/dns"},
		Note:       `run.sh SRC="$ROOT/.corpus/simpleinfra/terraform/dns".`,
	},
	{
		Name:       "corpus-sqs-basic",
		ConfigDirs: []string{".corpus/sqs/examples/complete"},
		Note:       `run.sh SRC_EXAMPLE="$ROOT/.corpus/sqs/examples/complete" - the estate name says "basic", the pinned module's own example directory is "complete" (script header is explicit about this).`,
	},
	{
		// ConfigDirs is empty on purpose: modules/base and modules/server
		// each carry a relative symlink that only resolves once a
		// "backend" sibling is materialized (sumaform's own pick-a-backend
		// convention), so this estate is scanned through
		// prepareSumaformModules (sumaform.go) instead of a plain
		// check.Load(ConfigDirs...) - see that file's doc comment.
		Name:       "corpus-sumaform-aws",
		ConfigDirs: nil,
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/sumaform"; copy_estate() rsyncs the whole modules/ and backend_modules/ trees (broader than this estate's own reach), but write_main_tf()'s heredoc calls only module "base" (source "./modules/base") and module "server" (source "./modules/server"); "ln -sf ../backend_modules/aws/ modules/backend" selects the aws backend package server references as ./backend - the two module calls, resolved through that backend, are this estate's real reach, not the whole rsynced tree (which also holds sumaform's other, unrelated backend packages: salt, prometheus, ...). See sumaform.go for why the two directories are copied rather than check.Load'd from .corpus directly.`,
	},
	{
		Name:       "corpus-vpc-complete",
		ConfigDirs: []string{".corpus/vpc/examples/complete"},
		Note:       `run.sh SRC_EXAMPLE="$ROOT/.corpus/vpc/examples/complete".`,
	},
	{
		Name:       "corpus-xancloud-iac",
		ConfigDirs: []string{".corpus/xancloud-iac/blueprints/landing-zone-basic"},
		ScanScript: true,
		Note:       `run.sh SRC="$ROOT/.corpus/xancloud-iac"; the estate is blueprints/landing-zone-basic (cd target for init/apply). The script's own heredocs write only providers.tf and versions.tf (provider/version wiring, no resources) on top of that directory's real files.`,
	},
	{
		Name:       "reference-ec2-vpc",
		ConfigDirs: nil,
		ScanScript: true,
		Note:       `The "reference" lane: no external source (live/GAUNTLET.md - "the plainest hand-written reference shape, kept in this repository"). run.sh's resource_block()/resource_block_ami_replaced() heredocs are the entire estate: aws_vpc, aws_subnet, aws_internet_gateway, aws_security_group, aws_instance - five resources, no module.`,
	},
}
