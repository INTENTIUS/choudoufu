WIP of the Azure derivability audit (see RESUME.md in azure-lane-notes). The worker stopped on an API error mid-task.
Committed: 47efafbc7e (an unknown azapi body with known top-level tags is stamped at top level; tested).
Uncommitted at the stop, included in this WIP commit: identity/armid.go, resolve.go, azure_test.go edits and two
probe tests (zz_reach_probe*_test.go) from the unmapped-type classification. They are unreviewed and may not build or pass.
buckets.txt: the unmapped azurerm types grouped by reason (bucket.py, idcalls.py produced it). Never merge this branch.
