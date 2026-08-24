data "aws_caller_identity" "current" {}

data "terraform_remote_state" "foundation" {
  backend = "s3"
  config = {
    bucket         = var.state_bucket_name
    key            = "foundation/terraform.tfstate"
    region         = var.aws_region
    dynamodb_table = var.state_lock_table_name
    encrypt        = true
    profile        = var.aws_profile != "" ? var.aws_profile : null
  }
}

locals {
  oidc_subjects = merge(
    { external_secrets = "system:serviceaccount:external-secrets:external-secrets" },
    var.dns_provider == "route53" ? { external_dns = "system:serviceaccount:external-dns:external-dns" } : {}
  )
  tags = {
    "platform-idp:managed"      = "true"
    "platform-idp:installation" = var.installation_id
    "platform-idp:service"      = "shared"
    "platform-idp:owner"        = "platform-owners"
    "platform-idp:environment"  = "platform"
    "platform-idp:cost-scope"   = "shared"
  }
}
