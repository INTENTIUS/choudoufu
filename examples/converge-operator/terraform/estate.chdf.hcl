# The live configuration for this root, as a sidecar rather than a `live`
# block inside terraform{}, with its own estate name so it can run against a
# shared floci account without colliding with another example.
#
# No `record_store` block: this example runs from one long-lived working
# directory for the length of one demo, never a fresh-runner-per-invocation
# CI job, so the implied `local` record store is reused across every
# `chant`/`choudoufu` call in the run rather than starting cold each time.
# choudoufu's own rule still holds regardless — the record is never consulted
# for ownership, only for a faster plan; losing it costs a slower run and
# nothing else.
estate = "converge-operator-example"
