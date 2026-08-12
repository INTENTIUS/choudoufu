# Coverage: receipt (effect memory). See stateless/RECEIPTS.md for the full
# spec these two resources demonstrate — a receipt is a plain declared
# resource (no subcommands, no list/repair verbs), the executor never runs
# the effect the receipt stands for, and nothing may depend on a receipt's
# attributes (the leaf rule). Both are admitted through the same client-named
# path aws_s3_bucket and aws_iam_role use (the name is already in config), so
# neither needs anything new from the admission mechanism, and nothing in
# this estate depends on either — that absence is the point, not an
# oversight; see the leaf rule in RECEIPTS.md.
#
# RECEIPTS.md, "Two flavors; prefer the simpler": this estate carries one of
# each, side by side, so the doc's claim about which flavor is the default
# recommendation is backed by a fixture instead of only prose.

# ── Existence flavor (the default recommendation) ───────────────────────────
# For a run-once effect, the parameter's existence is the entire bit: the
# value is a constant that carries no information by design, so there is
# nothing in it to scrub and nothing in it to go stale. "will be created"
# means "will fire"; deleting the parameter out of band is how this flavor
# gets broken (delete/create, not overwrite), and the next plan proposing
# exactly that create again is the re-arm signal.
resource "aws_ssm_parameter" "demo_existence" {
  name  = "/tofu-receipts/stateless-e2e/demo-existence"
  type  = "String" # never SecureString — the value carries no information (RECEIPTS.md)
  value = "done"   # constant by design: existence is the bit, not the value

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_ssm_parameter.demo_existence"
  }
}

# ── Hash flavor (run-on-change only) ─────────────────────────────────────────
# For an effect that must re-run whenever its declared inputs change, the
# value is a hash of those inputs — never the inputs themselves, and never
# anything derived from the effect's output.
locals {
  # The effect's declared inputs: whatever configuration data determines
  # whether the (imagined) out-of-band effect this receipt stands for needs
  # to run again. Naming them in one local is what keeps the hash below
  # reproducible from configuration alone, with no cloud read involved.
  demo_effect_inputs = {
    bucket = aws_s3_bucket.data.bucket
    role   = aws_iam_role.app.name
  }
}

resource "aws_ssm_parameter" "demo_effect" {
  name  = "/tofu-receipts/stateless-e2e/demo-effect"
  type  = "String" # never SecureString — a hash is not a secret (RECEIPTS.md)
  value = sha256(jsonencode(local.demo_effect_inputs))

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_ssm_parameter.demo_effect"
  }
}
