# Examples

The index of `examples/`: each example is a small project in the repository
that you can clone and run.

| Example | What it shows |
|---|---|
| [Terralith migration](../live/TERRALITH-MIGRATION.md) | Splitting one large configuration into per-team estates by retagging, with no state surgery, and the measured plan-time difference |
| [`examples/ci-pipelines`](ci-pipelines/README.md) | The five CI jobs an estate needs, generated for GitHub, Forgejo and GitLab |
| [`examples/pipeline-governance`](pipeline-governance/README.md) | Branch rules that require the check and plan jobs and put an apply behind a reviewer |
| [`examples/record-store-bucket`](record-store-bucket/README.md) | A correct record store bucket, and the IAM policy for an estate's role |
| [`examples/record-store-cluster`](record-store-cluster/README.md) | Records namespace and Roles for an estate kept in a cluster |
| [`examples/record-store-azurerm`](record-store-azurerm/CONTRACT.md) | What an Azure storage account must have to hold records, and the read fence for one estate |
| [`examples/record-store-gcs`](record-store-gcs/CONTRACT.md) | What a record store GCS bucket must have, one per estate, and the IAM grant for an estate's identity |
| [`examples/cross-estate-dependency`](cross-estate-dependency/) | Two estates, one reading a value off the other's live resource |
| [`examples/live-mv-workbench`](live-mv-workbench/) | A notebook that splits a configuration by retagging, against the local emulator |
| [`examples/demo-terragrunt`](demo-terragrunt/README.md) | A stock three-unit Terragrunt repo run with choudoufu as its binary, one joint plan across units |
| [`examples/demo-atmos`](demo-atmos/README.md) | A stock three-component Atmos repo run with choudoufu as its command, one joint plan across components |

terragucci runs three of these in its pipelines, as scenarios on its
harness (INTENTIUS/terragucci#1296): `cross-estate-dependency` as
`tg-compose-cross-estate`, `demo-terragrunt` as `tg-compose-terragrunt` and
`demo-atmos` as `tg-compose-atmos`. The trees stay here, and each example's
README names its scenario.
