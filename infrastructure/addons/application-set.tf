resource "kubernetes_manifest" "platform_services_applicationset" {
  for_each = var.gitops_repository_url != "" && var.chart_repository_url != "" ? { enabled = true } : {}

  manifest = yamldecode(templatefile("${path.module}/templates/platform-services-applicationset.yaml.tftpl", {
    gitops_repository_url = var.gitops_repository_url
    chart_repository_url  = var.chart_repository_url
  }))

  depends_on = [helm_release.argo_cd]
}

resource "kubernetes_manifest" "argocd_gitops_repository_credentials" {
  for_each = var.gitops_repository_credentials_secret != "" ? { enabled = true } : {}
  manifest = yamldecode(templatefile("${path.module}/templates/argocd-repository-external-secret.yaml.tftpl", {
    repository_url = var.gitops_repository_url
    secret_name    = "platform-config-repository"
    remote_key     = var.gitops_repository_credentials_secret
  }))
  depends_on = [helm_release.argo_cd, helm_release.external_secrets]
}

resource "kubernetes_manifest" "argocd_chart_repository_credentials" {
  for_each = var.chart_repository_credentials_secret != "" ? { enabled = true } : {}
  manifest = yamldecode(templatefile("${path.module}/templates/argocd-repository-external-secret.yaml.tftpl", {
    repository_url = var.chart_repository_url
    secret_name    = "platform-charts-repository"
    remote_key     = var.chart_repository_credentials_secret
  }))
  depends_on = [helm_release.argo_cd, helm_release.external_secrets]
}
