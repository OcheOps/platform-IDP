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

resource "kubernetes_namespace_v1" "platform_system" {
  metadata { name = "platform-system" }
}

resource "kubernetes_service_account_v1" "database_provisioner" {
  metadata {
    name      = "database-provisioner"
    namespace = kubernetes_namespace_v1.platform_system.metadata[0].name
    annotations = {
      "eks.amazonaws.com/role-arn" = aws_iam_role.workload["database_provisioner"].arn
    }
    labels = {
      "app.kubernetes.io/name"       = "database-provisioner"
      "app.kubernetes.io/part-of"    = "platform-idp"
      "app.kubernetes.io/managed-by" = "OpenTofu"
    }
  }
  automount_service_account_token = true
}

# Identity used by the control plane to launch and observe per-request database
# Jobs. It can only manage Jobs and their egress policies in platform-system and
# never reads Secrets; database credentials stay behind the provisioner's IRSA role.
resource "kubernetes_service_account_v1" "platform_control_plane" {
  metadata {
    name      = "platform-control-plane"
    namespace = kubernetes_namespace_v1.platform_system.metadata[0].name
    labels = {
      "app.kubernetes.io/name"       = "platform-control-plane"
      "app.kubernetes.io/part-of"    = "platform-idp"
      "app.kubernetes.io/managed-by" = "OpenTofu"
    }
  }
  automount_service_account_token = false
}

resource "kubernetes_role_v1" "database_job_launcher" {
  metadata {
    name      = "database-job-launcher"
    namespace = kubernetes_namespace_v1.platform_system.metadata[0].name
  }
  rule {
    api_groups = ["batch"]
    resources  = ["jobs"]
    verbs      = ["get", "create", "patch"]
  }
  rule {
    api_groups = ["networking.k8s.io"]
    resources  = ["networkpolicies"]
    verbs      = ["get", "create", "patch"]
  }
}

resource "kubernetes_role_binding_v1" "database_job_launcher" {
  metadata {
    name      = "database-job-launcher"
    namespace = kubernetes_namespace_v1.platform_system.metadata[0].name
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "Role"
    name      = kubernetes_role_v1.database_job_launcher.metadata[0].name
  }
  subject {
    kind      = "ServiceAccount"
    name      = kubernetes_service_account_v1.platform_control_plane.metadata[0].name
    namespace = kubernetes_namespace_v1.platform_system.metadata[0].name
  }
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
    awsRegion = var.aws_region
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
