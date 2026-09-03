"""The move set, the dry-run preview parser, and the guard over an arbitrary
carve. All pure: the dry-run text is the exact block
internal/command/views/live_mv.go renders ("  %-14s %s" rows, the
"Would move ... Nothing was written (-dry-run)" headline), and the refusal is
the tfdiags "Error:" shape, so the parser is pinned to real bytes, not a
paraphrase."""

from __future__ import annotations

import json
import unittest

from tlmig import moveset, verify


# The dry-run block for a cross-estate move, exactly as the view renders it.
def dry_run_block(frm: str, to: str, addr: str, rtype: str = "aws_iam_policy", live_id: str = "arn:aws:iam::354867293429:policy/p") -> str:
    return (
        "\n"
        "Would move one live resource into this estate. Nothing was written (-dry-run).\n"
        "\n"
        f"  from estate    {frm}\n"
        f"  to estate      {to}\n"
        f'  tofu-estate    "{frm}" -> "{to}"\n'
        f"  resource type  {rtype}\n"
        f"  live ID        {live_id}\n"
        f"  old address    {addr}\n"
        f"  new address    {addr}\n"
        f'  tofu-address   "{addr}" -> "{addr}"\n'
        "  found by       listing every aws_iam_policy and reading its ownership markers\n"
        "\n"
        "Rerun without -dry-run to write it. Everything above was read from the live system; nothing was changed.\n"
    )


# A tfdiags refusal, as live-mv prints one and exits nonzero.
REFUSAL = (
    "\n"
    "Error: Address not declared in this estate\n"
    "\n"
    "  aws_iam_policy.team_a is not declared in estate tl-team-a. Move the\n"
    "  resource block into this estate's configuration before moving its marker.\n"
)


class LoadCarve(unittest.TestCase):
    def test_reads_the_move_set_and_ignores_extra_fields(self):
        doc = {"moves": [
            {"address": "aws_iam_role.team_a", "from": "tl-mono", "to": "tl-team-a",
             "children": ["aws_iam_role_policy.team_a_inline"], "module": "teams", "rule": "by-prefix"},
            {"address": "aws_iam_policy.team_a", "from": "tl-mono", "to": "tl-team-a"},
        ]}
        cs = moveset.load_carve(json.dumps(doc))
        self.assertEqual(len(cs.moves), 2)
        self.assertEqual(cs.moves[0].children, ("aws_iam_role_policy.team_a_inline",))
        self.assertEqual(cs.moves[1].children, ())
        self.assertEqual(cs.source_estates, ("tl-mono",))
        self.assertEqual(cs.dest_estates, ("tl-team-a",))
        self.assertEqual(cs.estates, ("tl-mono", "tl-team-a"))

    def test_estates_dedup_across_a_multi_hop_carve(self):
        doc = {"moves": [
            {"address": "a", "from": "mono", "to": "team-a"},
            {"address": "b", "from": "mono", "to": "team-b"},
            {"address": "c", "from": "team-a", "to": "team-b"},
        ]}
        cs = moveset.load_carve(json.dumps(doc))
        self.assertEqual(set(cs.estates), {"mono", "team-a", "team-b"})
        self.assertEqual(len(cs.estates), 3)
        self.assertEqual({m.address for m in cs.moves_to("team-b")}, {"b", "c"})

    def test_a_rename_carries_new_address(self):
        doc = {"from": "mono", "estates": ["team-a"], "moves": [
            {"address": "aws_iam_role.old", "from": "mono", "to": "team-a", "new_address": "aws_iam_role.new"}]}
        cs = moveset.load_carve(json.dumps(doc))
        self.assertEqual(cs.moves[0].address, "aws_iam_role.old")
        self.assertEqual(cs.moves[0].new_address, "aws_iam_role.new")
        self.assertEqual(cs.moves[0].target, "aws_iam_role.new")
        # a pure retag: target falls back to the address
        pure = moveset.load_carve(json.dumps({"moves": [{"address": "a", "from": "x", "to": "y"}]}))
        self.assertEqual(pure.moves[0].target, "a")

    def test_malformed_is_refused_not_guessed(self):
        for bad, why in [
            ("not json", "invalid JSON"),
            ('{"moves": {}}', "moves array"),
            ('{"moves": []}', "no moves"),
            ('{"moves": [{"address": "a", "from": "x"}]}', "missing to"),
            ('{"moves": [{"address": "a", "from": "x", "to": "x"}]}', "same estate"),
            ('{"moves": [{"address": "", "from": "x", "to": "y"}]}', "empty address"),
        ]:
            with self.assertRaises(ValueError, msg=why):
                moveset.load_carve(bad)


class ParseDryRun(unittest.TestCase):
    def test_the_two_tag_writes_and_the_fields(self):
        move = moveset.CarveMove("aws_iam_policy.team_a", "tl-mono", "tl-team-a", children=("aws_iam_role_policy.team_a_inline",))
        pv = moveset.parse_dry_run(dry_run_block("tl-mono", "tl-team-a", "aws_iam_policy.team_a"), move=move)
        self.assertTrue(pv.ok)
        self.assertFalse(pv.written)
        self.assertEqual(pv.from_estate, "tl-mono")
        self.assertEqual(pv.to_estate, "tl-team-a")
        self.assertEqual(pv.type, "aws_iam_policy")
        self.assertEqual(pv.address, "aws_iam_policy.team_a")
        self.assertEqual(pv.children, ("aws_iam_role_policy.team_a_inline",))
        self.assertEqual(
            [(t.key, t.frm, t.to) for t in pv.tag_writes],
            [("tofu-estate", "tl-mono", "tl-team-a"), ("tofu-address", "aws_iam_policy.team_a", "aws_iam_policy.team_a")],
        )

    def test_as_event_uses_from_as_a_real_key(self):
        pv = moveset.parse_dry_run(dry_run_block("mono", "team-a", "aws_iam_role.r"))
        ev = pv.as_event()
        self.assertEqual(ev["tag_writes"][0], {"key": "tofu-estate", "from": "mono", "to": "team-a"})
        self.assertIsNone(ev["refusal"])
        self.assertIs(ev["written"], False)
        # round-trips through JSON the way the event feed writes it
        self.assertEqual(json.loads(json.dumps(ev))["tag_writes"][0]["from"], "mono")

    def test_a_refusal_is_captured_and_leaves_the_writes_empty(self):
        move = moveset.CarveMove("aws_iam_policy.team_a", "tl-mono", "tl-team-a")
        pv = moveset.parse_dry_run(REFUSAL, move=move)
        self.assertFalse(pv.ok)
        self.assertIsNotNone(pv.refusal)
        self.assertEqual(pv.refusal.summary, "Address not declared in this estate")
        self.assertIn("not declared in estate tl-team-a", pv.refusal.detail)
        self.assertEqual(pv.tag_writes, ())
        # the move still names the address even though no block was printed
        self.assertEqual(pv.address, "aws_iam_policy.team_a")
        self.assertEqual(pv.as_event()["refusal"]["summary"], "Address not declared in this estate")


class SetVerdict(unittest.TestCase):
    CS = moveset.load_carve(json.dumps({"moves": [
        {"address": "aws_iam_role.team_a", "from": "mono", "to": "team-a"},
        {"address": "aws_iam_policy.team_a", "from": "mono", "to": "team-a"},
    ]}))

    def _clean(self):
        return verify.parse_plan("No changes. Your infrastructure matches the configuration.\n")

    def _dirty_destroy(self):
        return verify.parse_plan("  # aws_iam_role_policy.team_a_inline will be destroyed\n\nPlan: 0 to add, 0 to change, 1 to destroy.\n")

    def test_ok_when_all_clean_and_all_landed(self):
        per_estate = {"mono": self._clean(), "team-a": self._clean()}
        landed = {"aws_iam_role.team_a": (True, "team-a"), "aws_iam_policy.team_a": (True, "team-a")}
        v = moveset.compose(self.CS, per_estate, landed)
        self.assertTrue(v.ok)
        self.assertTrue(all(m.ok for m in v.per_move))

    def test_a_dropped_child_makes_the_destination_dirty_and_the_set_fails(self):
        # the child orphaned under team-a, whose plan now proposes a destroy
        per_estate = {"mono": self._clean(), "team-a": self._dirty_destroy()}
        landed = {"aws_iam_role.team_a": (True, "team-a"), "aws_iam_policy.team_a": (True, "team-a")}
        v = moveset.compose(self.CS, per_estate, landed)
        self.assertFalse(v.ok)
        self.assertFalse(v.all_clean)
        self.assertTrue(v.all_landed)

    def test_a_tag_that_did_not_land_fails_even_with_clean_plans(self):
        per_estate = {"mono": self._clean(), "team-a": self._clean()}
        landed = {"aws_iam_role.team_a": (True, "team-a"), "aws_iam_policy.team_a": (False, None)}
        v = moveset.compose(self.CS, per_estate, landed)
        self.assertFalse(v.ok)
        self.assertTrue(v.all_clean)
        self.assertFalse(v.all_landed)
        did_not = [m for m in v.per_move if not m.landed][0]
        self.assertEqual(did_not.address, "aws_iam_policy.team_a")
        self.assertIsNone(did_not.live_estate)


if __name__ == "__main__":
    unittest.main()
