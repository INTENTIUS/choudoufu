# #178's local-values fix, module-var variant (RESOURCE_MODULEVAR): a
# managed resource's attribute is passed into a module call as an
# argument, and the resource inside the module reads it through var.*
# rather than through a local. The reference has to resolve the same way
# in either position - a module boundary is not a reason to evaluate the
# whole argument expression instead of the one attribute asked for.

resource "aws_route53_zone" "public" {
  name = "example.com"
}

module "dns" {
  source  = "./dns"
  zone_id = aws_route53_zone.public.zone_id
}
