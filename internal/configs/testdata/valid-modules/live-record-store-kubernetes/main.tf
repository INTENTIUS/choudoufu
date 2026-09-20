terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      namespace      = "tofu-records-my-estate"
      config_path    = "/home/ci/.kube/config"
      config_context = "prod"
      host           = "https://cluster.example:6443"
      insecure       = false

      exec {
        api_version = "client.authentication.k8s.io/v1beta1"
        command     = "aws"
        args        = ["eks", "get-token", "--cluster-name", "prod"]
        env = {
          AWS_PROFILE = "ci"
        }
      }
    }
  }
}
