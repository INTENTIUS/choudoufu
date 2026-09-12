# The consumer estate.
#
# A second estate, not a second root in the first one's estate. Sharing one
# `tofu-estate` value across two roots is mutual destruction: the sweep is
# estate-scoped rather than root-scoped, so each root's plan would see the
# other root's resources as `undeclared_tagged` and propose destroying every
# one of them. The README says this at length under "Why two estates".
estate = "cross-estate-service"
