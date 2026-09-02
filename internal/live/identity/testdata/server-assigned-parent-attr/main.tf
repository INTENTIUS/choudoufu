# The other boundary, and the one that matters most: the attribute IS in
# the provider's schema, but the parent is not concrete.
#
# aws_instance and aws_api_gateway_rest_api are both ServerAssigned rows -
# EC2 mints i-…, API Gateway mints the REST API id - so neither ever
# resolves CONCRETE and neither of their live objects is in the projection
# before a formula renders. public_ip and root_resource_id stay refused,
# and the class is what refuses them, not the absence of a schema entry:
# the test hands both attributes to the resolver in the schema map.

resource "aws_instance" "web" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"
}

resource "aws_api_gateway_rest_api" "api" {
  name = "public"
}

resource "aws_iam_group" "by_ip" {
  name = "logs-${aws_instance.web.public_ip}"
}

resource "aws_iam_group" "by_root" {
  name = "logs-${aws_api_gateway_rest_api.api.root_resource_id}"
}
