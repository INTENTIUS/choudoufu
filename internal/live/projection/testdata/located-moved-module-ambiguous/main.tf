# Two honoured `moved` statements land on the same destination module, and
# both of their origins carry a located record: there is no way to say
# which prior record is this instance's, so the lookup must refuse rather
# than pick one - the record-store analogue of "one marker value for two
# declared addresses".

module "thing_renamed" {
  source = "./child"
}

moved {
  from = module.thing_a
  to   = module.thing_renamed
}

moved {
  from = module.thing_b
  to   = module.thing_renamed
}
