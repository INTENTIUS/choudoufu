import unittest
from unittest import mock

from tlmig import config, govern, guard

CLEAN = "No changes. Your infrastructure matches the configuration.\n"


def _result(stdout: str, rc: int = 0) -> guard.Result:
    return guard.Result(argv=["fake"], returncode=rc, stdout=stdout, stderr="", seconds=0.0)


class ReadCarve(unittest.TestCase):
    """read_carve assembles the four reads into one verdict; the cloud is
    faked at the guard boundary so the assembly is tested without an
    account, and every call still goes through the guard's own functions."""

    def setUp(self):
        self.cfg = config.Config(run_id="t3st", run_dir=config.pathlib.Path("/tmp/tlmig-test"), binary="choudoufu")

    def _aws(self, tags_estate: str):
        def fake_aws(cfg, *args, **kw):
            if "list-role-tags" in args:
                return _result('{"Tags":[{"Key":"tofu-estate","Value":"%s"},{"Key":"tofu-address","Value":"aws_iam_role.team"}]}' % tags_estate)
            if "list-role-policies" in args:
                return _result('{"PolicyNames":["team-inline"]}')
            if "list-attached-role-policies" in args:
                return _result('{"AttachedPolicies":[{"PolicyName":"x","PolicyArn":"arn:aws:iam::aws:policy/X"}]}')
            raise AssertionError(f"unexpected aws call {args}")
        return fake_aws

    def test_clean_carve_is_ok(self):
        with mock.patch.object(guard, "aws", self._aws("team-b")), \
             mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: _result(CLEAN)):
            v = govern.read_carve(self.cfg, "team-role", "mono", "team-b", expected_inline=("team-inline",))
        self.assertTrue(v.ok)
        self.assertEqual(v.moved_estates, {"team-role": "team-b"})
        self.assertEqual(v.children_kept, {"team-role": ("team-inline",)})

    def test_tag_still_on_source_fails(self):
        with mock.patch.object(guard, "aws", self._aws("mono")), \
             mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: _result(CLEAN)):
            v = govern.read_carve(self.cfg, "team-role", "mono", "team-b")
        self.assertFalse(v.ok)

    def test_a_failed_plan_read_is_an_error_not_a_verdict(self):
        with mock.patch.object(guard, "aws", self._aws("team-b")), \
             mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: _result("boom", rc=1)):
            with self.assertRaises(guard.GuardError):
                govern.read_carve(self.cfg, "team-role", "mono", "team-b")


if __name__ == "__main__":
    unittest.main()
