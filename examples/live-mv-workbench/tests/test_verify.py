import unittest
from tlmig import verify

BREAK_SOURCE = """Owned and undeclared: 2 live resources will be destroyed

OpenTofu will perform the following actions:

  # aws_iam_instance_profile.team_0001_profile will be destroyed
  # aws_iam_role.team_0001_role will be destroyed
  # aws_iam_role_policy.team_0001_inline will be destroyed
  # aws_iam_role_policy_attachment.team_0001_custom_attach will be destroyed
  # aws_iam_role_policy_attachment.team_0001_managed_attach will be destroyed

Plan: 0 to add, 0 to change, 5 to destroy.
"""
BREAK_DEST = """Unowned: 2 live resources at declared names carry no marker for this estate
  aws_iam_instance_profile.team_0001_profile [UNOWNED]
  aws_iam_role.team_0001_role [UNOWNED]

  # aws_iam_role.team_0001_role will be created

Plan: 3 to add, 0 to change, 0 to destroy.
"""
CLEAN = "No changes. Your infrastructure matches the configuration.\n"


class PlanParsing(unittest.TestCase):
    def test_break_source_is_not_clean(self):
        v = verify.parse_plan(BREAK_SOURCE)
        self.assertFalse(v.leaves_nothing_behind)
        self.assertEqual(v.destroy, 5)
        self.assertEqual(v.owned_undeclared, 2)
        self.assertIn("aws_iam_role_policy.team_0001_inline", v.destroys)

    def test_break_destination_is_not_clean(self):
        v = verify.parse_plan(BREAK_DEST)
        self.assertFalse(v.owns_everything_it_declares)
        self.assertEqual(v.unowned, ("aws_iam_instance_profile.team_0001_profile", "aws_iam_role.team_0001_role"))

    def test_clean_plan(self):
        v = verify.parse_plan(CLEAN)
        self.assertTrue(v.clean and v.leaves_nothing_behind and v.owns_everything_it_declares)

    def test_disagreeing_plan_is_refused(self):
        with self.assertRaises(ValueError):
            verify.parse_plan("  # a.b will be destroyed\nPlan: 0 to add, 0 to change, 2 to destroy.\n")


class CliJson(unittest.TestCase):
    def test_role_tags(self):
        doc = '{"Tags":[{"Key":"tofu-estate","Value":"tl-team-1"},{"Key":"tofu-address","Value":"aws_iam_role.team_0001_role"}]}'
        self.assertEqual(verify.estate_of_role(doc), "tl-team-1")
        self.assertEqual(verify.address_of_role(doc), "aws_iam_role.team_0001_role")
        self.assertIsNone(verify.estate_of_role('{"Tags":[]}'))

    def test_children(self):
        self.assertEqual(verify.inline_policies('{"PolicyNames":["tl-team-0001-inline"]}'), ("tl-team-0001-inline",))
        self.assertEqual(verify.attached_policy_arns('{"AttachedPolicies":[{"PolicyName":"x","PolicyArn":"arn:aws:iam::aws:policy/X"}]}'), ("arn:aws:iam::aws:policy/X",))


class Carve(unittest.TestCase):
    def test_ok_only_when_all_four_hold(self):
        good = verify.CarveVerdict(
            source=verify.parse_plan(CLEAN), destination=verify.parse_plan(CLEAN),
            moved_estates={"tl-team-0001-role": "tl-team-1"}, children_kept={"tl-team-0001-role": ("tl-team-0001-inline",)},
            expected_estate="tl-team-1")
        self.assertTrue(good.ok)
        bad = verify.CarveVerdict(
            source=verify.parse_plan(BREAK_SOURCE), destination=verify.parse_plan(BREAK_DEST),
            moved_estates={"tl-team-0001-role": "tl-terralith"}, children_kept={"tl-team-0001-role": ("tl-team-0001-inline",)},
            expected_estate="tl-team-1")
        self.assertFalse(bad.ok)
        self.assertTrue(any("WRONG" in l for l in bad.lines()))


if __name__ == "__main__":
    unittest.main()


class Tightening(unittest.TestCase):
    """37's finding: a child dropped from both configurations orphans under
    the destination and shows there as a destroy; .ok must not read true."""

    DEST_DESTROY = """  # aws_iam_role_policy.team_0001_inline will be destroyed

Plan: 0 to add, 0 to change, 1 to destroy.
"""

    def test_a_destroy_on_the_destination_fails_the_carve(self):
        v = verify.CarveVerdict(
            source=verify.parse_plan(CLEAN), destination=verify.parse_plan(self.DEST_DESTROY),
            moved_estates={"r": "tl-team-1"}, children_kept={"r": ()}, expected_estate="tl-team-1")
        self.assertTrue(v.destination.owns_everything_it_declares, "the old check alone would have passed this")
        self.assertFalse(v.ok)

    def test_no_moved_resources_is_not_a_carve(self):
        v = verify.CarveVerdict(
            source=verify.parse_plan(CLEAN), destination=verify.parse_plan(CLEAN),
            moved_estates={}, children_kept={}, expected_estate="tl-team-1")
        self.assertFalse(v.ok)

    def test_describe(self):
        self.assertEqual(verify.parse_plan(CLEAN).describe(), "No changes.")
        self.assertIn("owned and undeclared: 2", verify.parse_plan(BREAK_SOURCE).describe())
