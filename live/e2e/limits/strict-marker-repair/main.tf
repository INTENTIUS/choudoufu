# Limits fixture: RuleStrictMarkerRepair (GitHub issue #365).
#
# `marker_repair = "never"` is one of the three settings this fork's schema
# defines, and it is the one HANDOFF.md names: "markers never repaired out of
# band (for estates where something else owns the tags, with ignore_changes
# honoured exactly as stock honours it)". The grammar for it landed with the
# strict block; the mechanism did not.
#
# The refusal is the point. Marker repair is not a code path a flag can turn
# off: nothing in this fork writes a marker tag onto a live object directly.
# internal/live/stamp rewrites the CONFIGURATION, and the repair of a drifted
# live tag is the provider's ordinary tags diff that follows. Suppressing it
# means suppressing that diff for the marker keys, which is what
# lifecycle { ignore_changes } does - and doing that safely needs somewhere
# else for a needs-discovery resource's identity to live, which is #365's
# `markers = record` toggle.
#
# So this setting is refused rather than accepted-and-ignored. Accepting it
# would tell an operator their estate's tags were safe from this tool while
# every plan carried on rewriting them. See live/LIMITATIONS.md,
# "strict-marker-repair".

terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = "never"
    }
  }
}
