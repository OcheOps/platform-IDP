output "argocd_expected_url" {
  description = "Expected URL after DNS propagation and certificate issuance; run the documented postflight checks."
  value       = "https://argocd.${var.base_domain}"
}
output "external_secrets_role_arn" { value = aws_iam_role.workload["external_secrets"].arn }
output "external_dns_role_arn" { value = try(aws_iam_role.workload["external_dns"].arn, null) }
