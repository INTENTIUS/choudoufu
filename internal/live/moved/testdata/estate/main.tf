# The five moved-block shapes the corpus actually contains, in one estate:
# a plain rename, a root-to-module refactor, a cross-module move, a module
# rename, and a count-expanded destination - both a resource's own count
# (the terraform-aws-modules idiom every shipped moved block lands on) and a
# module call's (issue #330: admitted on the same premise issue #195 already
# retired for a module's plain scalar count - see moved.go's Honourable doc
# comment).

module "queues" {
  source = "./modules/queues"
}

module "renamed" {
  source = "./modules/queues"
}

module "counted" {
  source = "./modules/queues"
  count  = 1
}

resource "aws_s3_bucket" "new" {
  bucket = "estate-data"
}

resource "aws_s3_bucket_versioning" "this" {
  count = 1

  bucket = aws_s3_bucket.new.id
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}

moved {
  from = aws_s3_bucket_versioning.legacy
  to   = aws_s3_bucket_versioning.this
}

moved {
  from = aws_sqs_queue.doi
  to   = module.queues.aws_sqs_queue.doi
}

moved {
  from = module.old_name
  to   = module.renamed
}

moved {
  from = module.gone.aws_sqs_queue.stray
  to   = module.renamed.aws_sqs_queue.stray
}

moved {
  from = aws_sqs_queue.solo
  to   = module.counted[0].aws_sqs_queue.doi
}
