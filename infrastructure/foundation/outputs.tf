output "cluster_name" { value = module.eks.cluster_name }
output "cluster_endpoint" { value = module.eks.cluster_endpoint }
output "vpc_id" { value = module.vpc.vpc_id }
output "database_address" { value = aws_db_instance.shared.address }
output "database_master_secret_arn" { value = aws_secretsmanager_secret.database_master.arn }
output "cluster_certificate_authority_data" {
  value     = module.eks.cluster_certificate_authority_data
  sensitive = true
}
output "oidc_provider" { value = module.eks.oidc_provider }
output "oidc_provider_arn" { value = module.eks.oidc_provider_arn }
