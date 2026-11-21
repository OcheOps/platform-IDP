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

The provisioning layer creates one deterministic IAM role per database-enabled
service. Its OIDC trust is restricted to the `service-secrets` service account
in that service's staging namespace, and its policy can read only the matching
Secrets Manager prefix. GitOps values carry the non-secret role ARN and region;
the shared chart creates the service account and namespaced `service-secrets`
`SecretStore`, then creates `ExternalSecret` mappings against it. A shared
cluster-wide store is deliberately forbidden because it would allow one service
namespace to request another service's secrets.

The database provisioning core derives deterministic PostgreSQL database and
login-role names, reconciles the role with no superuser, database-creation,
role-creation, or inheritance privileges, creates the owned database
idempotently, reuses one secret-backed password across interrupted attempts,
and publishes a single TLS-required `DATABASE_URL` value. The retry password
uses a separate provisioner-only secret identity; the service-visible secret
contains only `DATABASE_URL`. The
workflow pauses in `provisioning_database` after infrastructure apply and only
enters `deploying` after credential publication succeeds. The one-shot
`cmd/database-provisioner` runtime uses the AWS SDK for Go v2 and pgx from
inside EKS. IRSA grants it access only to the shared master secret, service
`DATABASE_URL` secrets, and provisioner-only password secrets. pgx serializes
reconciliation with an advisory lock, rejects ownership-marker collisions,
revokes `PUBLIC` database connectivity, and closes its admin connection after
each job. `charts/database-provisioner` runs a digest-pinned, non-root,
read-only Job with bounded retries and egress policy. The repository worker's
database Job reconciler (`internal/databasejob`) launches that Job for each
request in `provisioning_database` and records `deploying` only when the Job
reports `Complete`; an empty Secrets Manager container is never treated as a
deployable credential.

The deployment contract is rendered by `internal/gitops`: it produces one
digest-pinned `values.json` per staging service and an Argo CD ApplicationSet
that discovers only `services/*/staging`. The renderer emits External Secret
references, never secret values, and rejects non-HTTPS repository URLs or
mutable image tags. The deployment worker waits for the application CI image
attestation, commits these files to a deterministic GitOps pull request, and
records `awaiting_merge`. After merge, the runtime reconciler queries
authenticated Argo CD and marks the request ready only when the expected
application is synced and healthy. Installing the ApplicationSet remains next.

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
