resource "helm_release" "ingress_nginx" {
  name             = "ingress-nginx"
  namespace        = "ingress-nginx"
  create_namespace = true
  repository       = "https://kubernetes.github.io/ingress-nginx"
  chart            = "ingress-nginx"
  version          = "4.15.1"
  atomic           = true
  timeout          = 900
  values = [yamlencode({
    controller = {
      replicaCount      = 2
      service           = { annotations = { "service.beta.kubernetes.io/aws-load-balancer-type" = "nlb" } }
      admissionWebhooks = { enabled = true }
    }
  })]
}

resource "helm_release" "cert_manager" {
  name             = "cert-manager"
  namespace        = "cert-manager"
  create_namespace = true
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  version          = "v1.21.1"
  atomic           = true
  timeout          = 900
  values           = [yamlencode({ crds = { enabled = true } })]
}

resource "helm_release" "cluster_issuer" {
  name       = "platform-cluster-issuer"
  namespace  = "cert-manager"
  chart      = "${path.module}/../../charts/platform-bootstrap"
  atomic     = true
  timeout    = 300
  depends_on = [helm_release.cert_manager]
  values = [yamlencode({
    acmeEmail = var.acme_email
  })]
}

resource "helm_release" "external_secrets" {
  name             = "external-secrets"
  namespace        = "external-secrets"
  create_namespace = true
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = "2.5.0"
  atomic           = true
  timeout          = 900
  values = [yamlencode({
    installCRDs = true
    serviceAccount = {
      create      = true
      name        = "external-secrets"
      annotations = { "eks.amazonaws.com/role-arn" = aws_iam_role.workload["external_secrets"].arn }
    }
  })]
}

resource "helm_release" "external_dns" {
  name             = "external-dns"
  namespace        = "external-dns"
  create_namespace = true
  repository       = "https://kubernetes-sigs.github.io/external-dns"
  chart            = "external-dns"
  version          = "1.20.0"
  atomic           = true
  timeout          = 900
  values = [yamlencode(merge({
    provider      = { name = var.dns_provider }
    domainFilters = [var.base_domain]
    policy        = "sync"
    registry      = "txt"
    txtOwnerId    = var.installation_id
    serviceAccount = {
      create      = true
      name        = "external-dns"
      annotations = var.dns_provider == "route53" ? { "eks.amazonaws.com/role-arn" = aws_iam_role.workload["external_dns"].arn } : {}
    }
    }, var.dns_provider == "cloudflare" ? {
    env = [{ name = "CF_API_TOKEN", valueFrom = { secretKeyRef = { name = var.cloudflare_token_secret_name, key = "api-token" } } }]
  } : {}))]
}

resource "helm_release" "argo_cd" {
  name             = "argo-cd"
  namespace        = "argocd"
  create_namespace = true
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = "9.5.17"
  atomic           = true
  timeout          = 900
  depends_on       = [helm_release.ingress_nginx, helm_release.cluster_issuer]
  values = [yamlencode({
    global  = { domain = "argocd.${var.base_domain}" }
    configs = { params = { "server.insecure" = true } }
    server = {
      ingress = {
        enabled          = true
        ingressClassName = "nginx"
        hostname         = "argocd.${var.base_domain}"
        annotations      = { "cert-manager.io/cluster-issuer" = "letsencrypt" }
        tls              = true
      }
    }
  })]
}
