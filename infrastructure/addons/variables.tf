variable "installation_id" { type = string }
variable "aws_region" { type = string }
variable "aws_profile" { type = string }
variable "state_bucket_name" { type = string }
variable "state_lock_table_name" { type = string }
variable "dns_provider" {
  type = string
  validation {
    condition     = contains(["route53", "cloudflare"], var.dns_provider)
    error_message = "dns_provider must be route53 or cloudflare."
  }
}
variable "dns_zone_id" { type = string }
variable "base_domain" { type = string }
variable "acme_email" { type = string }
variable "cloudflare_token_secret_name" {
  type      = string
  default   = ""
  sensitive = true
}
variable "gitops_repository_url" {
  type        = string
  default     = ""
  description = "HTTPS URL of the private platform-config GitOps repository. When set with chart_repository_url, installs the ApplicationSet."
}
variable "chart_repository_url" {
  type        = string
  default     = ""
  description = "HTTPS URL of the repository containing the shared service chart."
}
