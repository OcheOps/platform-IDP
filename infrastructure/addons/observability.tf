resource "helm_release" "loki" {
  name             = "loki"
  namespace        = "observability"
  create_namespace = true
  repository       = "https://grafana-community.github.io/helm-charts"
  chart            = "loki"
  version          = "18.11.2"
  atomic           = true
  timeout          = 900
  values = [yamlencode({
    deploymentMode = "SingleBinary"
    loki = {
      auth_enabled = false
      commonConfig = { replication_factor = 1 }
      schemaConfig = {
        configs = [{
          from         = "2024-04-01"
          store        = "tsdb"
          object_store = "filesystem"
          schema       = "v13"
          index        = { prefix = "loki_index_", period = "24h" }
        }]
      }
      storage = { type = "filesystem" }
      limits_config = {
        retention_period        = "168h"
        ingestion_rate_mb       = 1
        ingestion_burst_size_mb = 2
      }
      compactor = {
        retention_enabled    = true
        working_directory    = "/var/loki/compactor"
        delete_request_store = "filesystem"
      }
    }
    singleBinary = {
      replicas = 1
      persistence = {
        enabled              = false
        dataVolumeParameters = { emptyDir = { sizeLimit = "20Gi" } }
      }
      resources = {
        requests = { cpu = "100m", memory = "256Mi" }
        limits   = { cpu = "1", memory = "1Gi" }
      }
    }
    read         = { replicas = 0 }
    write        = { replicas = 0 }
    backend      = { replicas = 0 }
    chunksCache  = { enabled = false }
    resultsCache = { enabled = false }
    gateway = {
      enabled = true
      resources = {
        requests = { cpu = "25m", memory = "64Mi" }
        limits   = { cpu = "250m", memory = "256Mi" }
      }
    }
  })]
}

resource "helm_release" "alloy" {
  name             = "alloy"
  namespace        = "observability"
  create_namespace = true
  repository       = "https://grafana.github.io/helm-charts"
  chart            = "alloy"
  version          = "1.12.0"
  atomic           = true
  timeout          = 600
  depends_on       = [helm_release.loki]
  values = [yamlencode({
    controller = { type = "daemonset" }
    alloy = {
      mounts = { varlog = true }
      resources = {
        requests = { cpu = "50m", memory = "128Mi" }
        limits   = { cpu = "500m", memory = "512Mi" }
      }
      configMap = {
        content = <<-EOT
          local.file_match "pod_logs" {
            path_targets = [{
              __path__ = "/var/log/pods/*/*/*.log",
              job      = "kubernetes-pods",
            }]
          }

          loki.source.file "pod_logs" {
            targets    = local.file_match.pod_logs.targets
            forward_to = [loki.process.pod_logs.receiver]
          }

          loki.process "pod_logs" {
            stage.cri {}
            stage.regex {
              source     = "filename"
              expression = "/var/log/pods/(?P<namespace>[^_]+)_(?P<pod>[^_]+)_[^/]+/(?P<container>[^/]+)/.*\\.log"
            }
            stage.labels {
              values = {
                namespace = "",
                pod       = "",
                container = "",
                service   = "namespace",
              }
            }
            forward_to = [loki.write.default.receiver]
          }

          loki.write "default" {
            endpoint {
              url = "http://loki-gateway.observability.svc.cluster.local/loki/api/v1/push"
            }
          }
        EOT
      }
    }
  })]
}

resource "helm_release" "kube_prometheus_stack" {
  name             = "kube-prometheus-stack"
  namespace        = "observability"
  create_namespace = true
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  version          = "88.0.1"
  atomic           = true
  timeout          = 1200
  depends_on       = [helm_release.loki, helm_release.cluster_issuer, helm_release.ingress_nginx]
  values = [yamlencode({
    alertmanager = { enabled = false }
    prometheus = {
      prometheusSpec = {
        retention                               = "7d"
        retentionSize                           = "20GB"
        serviceMonitorSelectorNilUsesHelmValues = false
        podMonitorSelectorNilUsesHelmValues     = false
        serviceMonitorNamespaceSelector         = {}
        podMonitorNamespaceSelector             = {}
        resources = {
          requests = { cpu = "200m", memory = "512Mi" }
          limits   = { cpu = "2", memory = "2Gi" }
        }
      }
    }
    grafana = {
      resources = {
        requests = { cpu = "100m", memory = "128Mi" }
        limits   = { cpu = "1", memory = "512Mi" }
      }
      additionalDataSources = [{
        name      = "Loki"
        type      = "loki"
        uid       = "loki"
        url       = "http://loki-gateway.observability.svc.cluster.local"
        access    = "proxy"
        isDefault = false
      }]
      persistence = { enabled = false }
    }
    prometheusOperator = {
      resources = {
        requests = { cpu = "100m", memory = "128Mi" }
        limits   = { cpu = "500m", memory = "512Mi" }
      }
    }
    kube-state-metrics = {
      resources = {
        requests = { cpu = "25m", memory = "64Mi" }
        limits   = { cpu = "250m", memory = "256Mi" }
      }
    }
    prometheus-node-exporter = {
      resources = {
        requests = { cpu = "25m", memory = "32Mi" }
        limits   = { cpu = "250m", memory = "128Mi" }
      }
    }
  })]
}
