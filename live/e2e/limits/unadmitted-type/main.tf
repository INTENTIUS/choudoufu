# Limits fixture: RuleUnadmittedType.
#
# aws_instance held this fixture's place until the EC2 core batch (issue
# #65) admitted it; aws_nat_gateway takes over as a stable replacement
# rather than another type that same wave of ratification would
# immediately outgrow. It is a real, non-logical, server-assigned type in
# live/SURVEY.md's curated 68 (marker path, blocked-emulator: the provider
# reads subnet_id out of the NatGatewayAddresses list, which floci returns
# empty, so an imported gateway loses its subnet and every plan proposes
# replacement — choudoufu#26), deliberately left out of the EC2 core
# batch's own instances/EBS/ENI scope (see
# live/e2e/estates/ec2-core/README.md's "Rejected, and out of scope") and
# outside every batch issue #65 names next (RDS, ECS/EKS, API Gateway,
# DynamoDB periphery, Route53 remainder), so no batch already on the
# roadmap is expected to pull it out from under this fixture the way
# aws_instance was pulled. See live/LIMITATIONS.md.

resource "aws_nat_gateway" "web" {
  subnet_id     = "subnet-12345678"
  allocation_id = "eipalloc-12345678"
}
