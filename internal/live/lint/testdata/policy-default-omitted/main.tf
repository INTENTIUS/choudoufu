# Clean-pass fixture: a policy block that only sets declared_tagged. The
# other three quadrants - including undeclared_tagged, whose default is
# "delete", today's unscoped sweep - are left to their preset and must not
# be treated as if they had been explicitly assigned "delete": the scope
# rule only applies to a quadrant a configuration actually wrote "delete"
# into (see checkLivePolicy's ruling), so this fixture has no scope block
# and still has to lint clean.

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = "keep"
    }
  }
}
