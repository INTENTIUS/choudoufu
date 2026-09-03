import unittest

from tlmig import sweep as verify

PREFIX = "tlmig-9f3a1c"


class IamFilters(unittest.TestCase):
    def test_prefix_is_the_fence(self):
        roles = '{"Roles":[{"RoleName":"tlmig-9f3a1c-team-a-role"},{"RoleName":"tlmig-other-team-a-role"},{"RoleName":"prod-admin"}]}'
        policies = '{"Policies":[{"PolicyName":"tlmig-9f3a1c-team-b-policy"},{"PolicyName":"AdministratorAccess"}]}'
        profiles = '{"InstanceProfiles":[{"InstanceProfileName":"tlmig-9f3a1c-profile"}]}'
        found = verify.iam_leftovers(PREFIX, roles, policies, profiles)
        self.assertEqual([str(i) for i in found], [
            "iam role tlmig-9f3a1c-team-a-role",
            "iam policy tlmig-9f3a1c-team-b-policy",
            "instance profile tlmig-9f3a1c-profile",
        ])

    def test_empty_account_is_clean(self):
        self.assertEqual(verify.iam_leftovers(PREFIX, '{"Roles":[]}', '{"Policies":[]}', '{"InstanceProfiles":[]}'), [])


class Ec2AndTagging(unittest.TestCase):
    def test_terminated_instances_are_not_leftovers(self):
        doc = ('{"Reservations":[{"Instances":['
               '{"InstanceId":"i-1","State":{"Name":"terminated"}},'
               '{"InstanceId":"i-2","State":{"Name":"running"}}]}]}')
        found = verify.ec2_leftovers("tlmig-9f3a1c-monolith", doc)
        self.assertEqual([i.name for i in found], ["i-2"])
        self.assertEqual(found[0].estate, "tlmig-9f3a1c-monolith")

    def test_tagging_index_skips_ec2_instances_it_lags_on(self):
        doc = ('{"ResourceTagMappingList":['
               '{"ResourceARN":"arn:aws:ec2:us-east-1:1:instance/i-1"},'
               '{"ResourceARN":"arn:aws:iam::1:role/tlmig-9f3a1c-x"}]}')
        found = verify.tagged_leftovers("e", doc)
        self.assertEqual([i.name for i in found], ["arn:aws:iam::1:role/tlmig-9f3a1c-x"])


class LogGroups(unittest.TestCase):
    def test_only_groups_under_the_run_prefix_count(self):
        doc = ('{"logGroups":[{"logGroupName":"/tlmig-9f3a1c/team-a/svc-1"},'
               '{"logGroupName":"/tlmig-9f3a1c/team-c/svc-3"},'
               '{"logGroupName":"/tlmig-9f3a1cX/other"},'
               '{"logGroupName":"/aws/lambda/prod"}]}')
        found = verify.log_group_leftovers(PREFIX, doc)
        self.assertEqual([i.name for i in found], ["/tlmig-9f3a1c/team-a/svc-1", "/tlmig-9f3a1c/team-c/svc-3"])

    def test_no_groups_is_clean(self):
        self.assertEqual(verify.log_group_leftovers(PREFIX, '{"logGroups":[]}'), [])


class LeftoversError(unittest.TestCase):
    def test_message_lists_every_item(self):
        err = verify.Leftovers([verify.Leftover("iam role", "a"), verify.Leftover("ec2 instance", "i-1", "e")])
        self.assertIn("2 resource(s)", str(err))
        self.assertIn("  - iam role a", str(err))
        self.assertIn("  - ec2 instance i-1 (tofu-estate=e)", str(err))


if __name__ == "__main__":
    unittest.main()
