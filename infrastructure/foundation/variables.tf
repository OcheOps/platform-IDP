variable "installation_id" { type = string }
variable "aws_region" { type = string }
variable "aws_profile" { type = string }
variable "github_organization" { type = string }
variable "dns_provider" { type = string }
variable "dns_zone_id" { type = string }
variable "base_domain" { type = string }
variable "vpc_cidr" { type = string }
variable "kubernetes_version" { type = string }
variable "node_instance_types" { type = list(string) }
variable "node_minimum_size" { type = number }
variable "node_maximum_size" { type = number }
variable "cluster_public_access_cidrs" { type = list(string) }

variable "database_instance_class" {
  type    = string
  default = "db.t4g.small"
}
