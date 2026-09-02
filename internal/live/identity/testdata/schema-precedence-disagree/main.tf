# aws_route is NOT reproduced by a schema (tools/row-gen/schemafirst.go
# classifies it "any-of": route_table_id plus three alternative
# destination_* arguments, one required schema route_table_id-only never
# reconstructs). This fixture pairs it with a schema that only names
# route_table_id, so TestSchemaPrecedenceKeepsRowWhenSchemaDisagrees can
# assert the ROW still wins - the ledger's own exception, held at runtime.

resource "aws_route" "example" {
  route_table_id         = "rtb-0123456789abcdef0"
  destination_cidr_block = "10.0.0.0/16"
}
