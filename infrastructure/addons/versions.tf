terraform {
  required_version = ">= 1.9.0"
  backend "s3" {}
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.52, < 7.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = ">= 2.17, < 3.0"
    }
  }
}

provider "aws" {
  region  = var.aws_region
  profile = var.aws_profile != "" ? var.aws_profile : null
  default_tags { tags = local.tags }
}

provider "helm" {
  kubernetes {
    host                   = data.terraform_remote_state.foundation.outputs.cluster_endpoint
    cluster_ca_certificate = base64decode(data.terraform_remote_state.foundation.outputs.cluster_certificate_authority_data)
    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", data.terraform_remote_state.foundation.outputs.cluster_name, "--region", var.aws_region]
      env         = var.aws_profile != "" ? { AWS_PROFILE = var.aws_profile } : {}
    }
  }
}
