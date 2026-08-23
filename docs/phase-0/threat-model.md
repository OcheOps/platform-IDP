# Threat Model

## Security objectives

- A compromised application cannot administer the platform or another service.
- A developer cannot bypass independent approval to provision staging resources.
- Pull-request code cannot exfiltrate infrastructure credentials or secrets.
- Secret values never enter Git, plans, portal responses, audit metadata, or
  normal logs.
- Destructive operations affect only precisely identified managed resources.
- Every privileged action is attributable and tamper-evident enough for a small
  engineering team to investigate.

## Assets

AWS account access, GitHub organization/repositories, OpenTofu state, platform
database, Secrets Manager values, database data, signing/webhook secrets, DNS
zones, container registry, audit history, and approval evidence.

## Trust boundaries

1. User browser to the portal/API.
2. GitHub OAuth/App/webhooks to the control plane.
3. GitHub Actions to AWS through OIDC.
4. Control-plane pods to AWS and Kubernetes APIs.
5. Argo CD from platform-config Git to Kubernetes.
6. External Secrets from Secrets Manager to namespaced Kubernetes Secrets.
7. Application and preview namespaces to shared cluster services and RDS.
8. Operator workstation to initial bootstrap.

## Threats and required controls

| Threat | Required controls |
|---|---|
| Stolen long-lived AWS credentials | Use short-lived OIDC/session credentials only; bootstrap uses the operator's existing AWS session and never persists it |
| Malicious or forged webhook | Verify GitHub signatures over the raw body, reject replays, validate installation/repository/org IDs, use idempotent delivery IDs |
| Approval bypass | GitHub branch protection, head-SHA and plan-digest binding, independent identities, authorization recheck before apply |
| Pull-request workflow credential theft | No AWS role or organization secrets for untrusted forks; least-privilege environments; immutable third-party action SHAs |
| Cross-service secret access | Per-service IAM roles and secret paths, namespace-scoped External Secrets, deny wildcard reads |
| Secret leakage in plan/log/UI | Accept references only; redact known patterns and provider output; mark sensitive OpenTofu outputs; never redisplay values |
| State disclosure or tampering | S3 encryption, public-access block, versioning, TLS, least-privilege state prefixes, DynamoDB locking, access logging |
| Confused-deputy GitHub/AWS action | Bind role trust to repository, organization, branch/environment, audience, and exact subject claims |
| Supply-chain compromise | Lock dependencies, pin action digests/SHAs, scan images and IaC, generate provenance/SBOM later without blocking the initial seam |
| Container breakout or lateral movement | Non-root containers, restricted pod security, network policies, workload identity, no host mounts or privileged pods |
| SQL tenant crossover | Unique database and role per service/environment; ownership markers; revoke/connect termination before drop |
| DNS takeover | Deterministic managed records, ownership TXT markers, scoped provider credentials, remove DNS before preview backend |
| Accidental broad deletion | Exact canonical ID plus managed-tag verification, plan display, two approvals, final staging snapshot, retention delay |
| Portal database corruption | Git/runtime remain sources of truth; backups; append-only audit constraints; rebuildable catalogue projections |
| Denial of wallet | Capacity enums, quotas, cost threshold, preview TTL, reconciliation backoff, rate limiting |

## Credential handling

- Operators provide their own GitHub App/OAuth, AWS, and DNS credentials.
- Bootstrap writes secret values directly to their destination and keeps only
  references in generated configuration.
- GitHub Actions uses AWS OIDC. No AWS access key is stored as a GitHub secret.
- Workloads use EKS workload identity/IRSA with service-specific roles.
- DNS credentials are scoped to the selected zone and stored in Secrets Manager.
- Logs and errors are redacted before leaving a trust boundary.

## Deferred risks

Single-account and shared-cluster/shared-RDS designs have a larger blast radius
than the planned multi-account production architecture. V1 is staging/preview
only. Network policy, IAM isolation, quotas, backups, and protected teardown
reduce risk but do not make it suitable for production workloads.

## Security acceptance checks

- Search generated repositories, config, plans, and logs for seeded canary
  secrets and find none.
- Prove a fork pull request receives neither AWS credentials nor service secrets.
- Prove a service role cannot read a sibling service's secret or database.
- Prove changed configuration invalidates approvals.
- Prove duplicate webhooks and retries do not duplicate resources.
- Prove teardown refuses resources with missing or mismatched ownership tags.
