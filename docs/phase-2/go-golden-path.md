# Go Golden Path

Phase 2 begins with one shared deployment contract. Every generated service
supplies values conforming to `contracts/schemas/helm-values.schema.json` and is
deployed with `charts/service`; generated repositories do not copy Kubernetes
manifests.

The chart provides:

- immutable digest-based images and restrictive pod/container security;
- liveness and readiness probes on the golden-path endpoints;
- explicit CPU and memory requests and limits plus bounded autoscaling;
- nginx ingress, cert-manager TLS, and ExternalDNS-compatible hostnames;
- namespace-scoped External Secret mappings without rendering secret values;
- Prometheus `ServiceMonitor` discovery and labels used by Alloy/Loki;
- ingress isolation to nginx and observability namespaces, plus egress limited
  to cluster DNS and PostgreSQL inside the Phase 1 `10.42.0.0/16` VPC; and
- a disruption budget when the minimum replica count is greater than one.

Render the current Go example locally with:

```bash
helm lint charts/service
helm template orders-api charts/service \
  --namespace svc-orders-api \
  --values contracts/examples/go-api-helm-values.json
```

The provisioning layer must create a `service-secrets` `SecretStore` in each
service namespace, backed by a service-specific IAM role and secret prefix. The
chart creates `ExternalSecret` mappings against that namespaced store. A shared
cluster-wide store is deliberately forbidden because it would allow one service
namespace to request another service's secrets.

The next slice renders the `go-chi-api` source repository, migration command,
Dockerfile, GitHub Actions workflow, and platform metadata against this chart.
