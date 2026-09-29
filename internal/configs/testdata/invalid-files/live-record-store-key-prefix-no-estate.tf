terraform {
  live {
    # No estate argument: the name is derived from the tofu-estate tags this
    # configuration stamps. Nothing here can then say whether the key_prefix
    # below is this estate's own records namespace or another estate's.
    record_store "s3" {
      bucket     = "my-records-bucket"
      key_prefix = "tofu-records/my-estate"
    }
  }
}
