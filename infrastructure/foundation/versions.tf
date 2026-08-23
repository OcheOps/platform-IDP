terraform {
  required_version = ">= 1.9.0"
  backend "s3" {}
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.52, < 7.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.7, < 4.0"
    }
  }
}

provider "aws" {
  region  = var.aws_region
  profile = var.aws_profile != "" ? var.aws_profile : null
  default_tags { tags = local.tags }
}
