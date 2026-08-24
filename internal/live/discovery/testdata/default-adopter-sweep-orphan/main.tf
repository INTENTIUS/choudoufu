# A configuration that declares neither side of any #305/#302 companion
# pair at all, so both admitted names are eligible for the estate-wide tag
# sweep ([sweepTypes] only excludes a type the configuration itself
# declares) - the shape [TestTaggingSweepDefaultSecurityGroupCompanionOrphanCarriesIDForward]
# needs to exercise sweepBindType's "not declared anywhere, carries the
# identity forward" branch through the real sweep path rather than the
# discovery-level scanType path TestDiscoverDefaultSecurityGroupAliasIsNotMalformed
# already covers.
resource "aws_sns_topic" "unrelated" {
  name = "unrelated"
}
