provider "aws" {
  region = "us-east-1"
}

module "child" {
  source = "./child"

  # A literal empty collection: OpenTofu resolves zero instances and raises
  # no diagnostic. It evaluates WHOLE, so this reaches
  # [resolver.evaluatedCollElements] and never [resolver.staticCollElems]'s
  # chase - #258's own reachability claim, pinned by
  # TestUnreadableConversionEmptyBranchIsUnreachableThroughAFixture.
  s = []
}
