resource "kubernetes_manifest" "platform_services_applicationset" {
  for_each = var.gitops_repository_url != "" && var.chart_repository_url != "" ? { enabled = true } : {}

  manifest = yamldecode(templatefile("${path.module}/templates/platform-services-applicationset.yaml.tftpl", {
    gitops_repository_url = var.gitops_repository_url
    chart_repository_url  = var.chart_repository_url
  }))

  depends_on = [helm_release.argo_cd]
}
