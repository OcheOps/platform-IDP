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

The deployment contract is rendered by `internal/gitops`: it produces one
digest-pinned `values.json` per staging service and an Argo CD ApplicationSet
that discovers only `services/*/staging`. The renderer emits External Secret
references, never secret values, and rejects non-HTTPS repository URLs or
mutable image tags. The deployment worker waits for the application CI image
attestation, commits these files to a deterministic GitOps pull request, and
verifies the exact values before marking the deployment request ready.
Installing the ApplicationSet and wiring authenticated Argo CD health
observations into request state remain next.

The `templates/go-chi-api` golden path is a buildable reference repository.
`internal/goldenpath.RenderGoChi` validates every declared input, copies the
template without following symlinks or overwriting a target, substitutes module
imports and platform metadata, and proves the rendered repository builds in its
tests.

Database-enabled values render the template's `/migrate` binary as an Argo CD
Sync hook in wave `-1`; External Secrets reconcile in wave `-2`, and the
Deployment follows in wave `0`. A failed migration therefore blocks rollout.
Applied migrations record a SHA-256 checksum and fail if an already-applied SQL
file is edited.

The generated CI workflow uses commit-pinned actions, tests and vets the source,
runs `govulncheck`, builds one image, scans that exact image with Trivy, and only
then publishes the same local artifact to ECR through GitHub OIDC. GitOps
promotion consumes the reported registry digest, not its commit-SHA tag.
