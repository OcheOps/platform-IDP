# Open-Source Internal Developer Platform Plan

## 1. Product vision

Build a fully self-hosted internal developer platform for small engineering
teams (roughly 5–30 developers) using AWS and GitHub.

The core experience is:

> A developer asks for a service, such as “Give me a Go API with PostgreSQL in
> staging,” and receives a production-shaped repository, AWS infrastructure,
> CI/CD, deployment, DNS, secrets, logs, metrics, and preview environments
> without needing to assemble those systems manually.

The platform is opinionated. It provides safe golden paths rather than acting
as a general-purpose Kubernetes configuration interface.

## 2. Target users

### Developer

- Creates services through the web portal.
- Owns application code and service-level configuration.
- Reviews requested capacity and cost.
- Sees deployments, health, logs, metrics, and preview environments.

### Engineering owner

- Reviews and approves a service's infrastructure request.
- Rightsizes CPU, memory, replicas, database allocation, and preview limits.
- Cannot satisfy the approval requirement when they are also the requester.

### Platform owner

- Bootstraps and operates the platform.
- Owns the shared Helm chart, policies, AWS foundation, and golden paths.
- Reviews infrastructure safety, operational fit, and cost.
- Approves provisioning and protected teardown changes.

### Viewer

- Has read-only access to the service catalogue and operational state.

GitHub organization membership and teams are the source of identity and role
mapping in v1.

## 3. Locked v1 scope

### Platform foundation

- One AWS account.
- One AWS region selected during installation.
- A platform-provisioned VPC and EKS cluster.
- Staging and pull-request preview environments.
- Production is not managed in the initial release.
- OpenTofu modules, kept Terraform-compatible where practical.
- Encrypted, versioned S3 remote state with DynamoDB state locking.
- GitHub Actions authenticating to AWS with OIDC and no long-lived AWS keys.

### Developer experience

- Web portal as the primary interface.
- A separate operator bootstrap CLI for installation.
- Git as the source of truth for desired state.
- GitHub-only source-control integration.
- Platform-generated repositories only; importing existing applications comes
  later.

### Golden paths

The first end-to-end vertical slice is:

- Go REST API using `chi`.
- Shared managed PostgreSQL.
- Production-ready multi-stage Dockerfile.
- Tests, linting, database migrations, structured logging, health checks, and
  metrics instrumentation.

After that vertical slice works, add:

- Node.js API using NestJS.
- Java API using Spring Boot.
- Next.js web application.

Every template must implement the same platform contract: build, test,
container image, health/readiness endpoints, configuration, secrets, metrics,
and deployment metadata. Database support is optional for templates where it
does not apply.

### Provisioning workflow

1. A developer completes a short portal form.
2. The control plane validates the request and calculates a pre-provision
   monthly estimate.
3. The platform creates an application repository from a versioned golden
   path.
4. The platform opens a pull request in the central platform-config repository.
5. GitHub Actions runs validation, `tofu plan`, policy checks, and cost
   estimation, then posts the result to the pull request.
6. One engineering owner and one platform owner must approve the change. The
   requester cannot approve their own request.
7. Merge runs `tofu apply` and updates the GitOps desired state.
8. Argo CD deploys the service and reports status to the platform.
9. The portal reconciles desired state with GitHub, OpenTofu outputs, AWS,
   Argo CD, and Kubernetes.

Reviewers can request or commit rightsizing changes before approval.

### Git and deployment model

- One generated GitHub repository per application.
- One central platform-config repository for environment definitions.
- GitHub Actions for application CI and infrastructure automation.
- Amazon ECR for container images.
- Argo CD for continuous delivery.
- Argo CD ApplicationSets discover service environment definitions.
- One platform-owned shared Helm chart encodes the deployment standard.
- Services provide compact values files instead of copied Kubernetes YAML.
- Application teams may configure approved fields such as CPU, memory, replica
  bounds, environment variables, secret references, ingress, health checks,
  and database options.
- Arbitrary Kubernetes resources and arbitrary Helm overrides are not allowed
  in v1.

### PostgreSQL

- One shared managed RDS PostgreSQL instance for staging and previews.
- A separate database and credentials for each service.
- A separate temporary database and credentials for each preview environment.
- Credentials stored in AWS Secrets Manager.
- External Secrets Operator synchronizes secrets into Kubernetes.
- Secret values never enter Git and are not redisplayed by the portal after
  creation.

### Preview environments

- Every application pull request automatically creates or updates a preview.
- Each preview receives its own namespace, deployment, URL, secrets, and
  temporary database when required.
- Closing or merging the pull request tears the preview down automatically.
- A configurable TTL removes abandoned previews.
- Preview creation does not require the two staging approvals.

### DNS and TLS

- Operators choose Route 53 or Cloudflare during bootstrap.
- Operators bring an existing hosted domain or zone.
- A DNS provider interface isolates provider-specific behavior.
- ExternalDNS manages records.
- cert-manager provisions and renews TLS certificates.
- Suggested URL structure:
  - `<service>.staging.<domain>`
  - `pr-<number>.<service>.preview.<domain>`

### Observability

- Prometheus for metrics.
- Grafana for dashboards.
- Loki for logs.
- Each golden path emits structured logs and baseline application metrics.
- Service catalogue pages link directly to filtered logs and dashboards.
- Distributed tracing is deferred.

### Cost estimates

- Show a pre-provision monthly estimate before a request is submitted.
- Include shared and incremental EKS, RDS, storage, load-balancer, network, and
  expected preview costs where they can be estimated.
- Clearly distinguish shared-platform costs from incremental service costs.
- Attach the estimate and its assumptions to the provisioning pull request.
- Tag provisioned resources by platform, service, owner, and environment so
  actual cost attribution can be added later.
- Actual AWS spend reporting is not part of v1.

### Service catalogue

Each service page shows:

- Owner and GitHub repository.
- Golden path and template version.
- Staging and preview URLs.
- Deployment and health status.
- CPU, memory, and replica configuration.
- PostgreSQL provisioning status when applicable.
- Logs and Grafana links.
- Estimated infrastructure cost.
- Recent deployments and active previews.

Git is the source of desired state. Live state is read or reconciled from AWS,
Argo CD, and Kubernetes. The portal database is not a competing source of
infrastructure truth.

### Protected deletion

- Permanent staging-service deletion starts in the portal and creates a
  teardown pull request.
- The pull request lists all resources and data that will be removed.
- It requires independent engineering-owner and platform-owner approval.
- The platform creates a final database snapshot.
- A configurable retention window applies before data destruction.
- Preview environments remain disposable and do not use this process.

## 4. Proposed architecture

### Control plane

- Go API and background reconcilers.
- Next.js/TypeScript web portal.
- PostgreSQL for users, role mappings, audit history, workflow metadata, and
  cached read models.
- Kubernetes Jobs for bounded long-running work where appropriate.
- Server-sent events for live provisioning and deployment progress.

The control plane uses reconciliation rather than relying solely on one-shot
requests. It continuously compares declared and observed state, reports drift,
and makes retries idempotent.

### Core logical modules

1. **Identity and authorization** — GitHub OAuth, organization restriction,
   team-to-role mapping, and authorization checks.
2. **Catalogue** — service metadata and an aggregated operational read model.
3. **Golden paths** — template catalogue, versioning, rendering, and repository
   creation.
4. **Provisioning** — service request validation, pull-request workflow,
   OpenTofu plans/outputs, and teardown orchestration.
5. **GitHub integration** — GitHub App installation, repositories, branches,
   pull requests, checks, teams, and webhooks.
6. **GitOps integration** — desired-state files, Argo CD applications, sync, and
   health state.
7. **Environment lifecycle** — staging and preview creation, reconciliation,
   TTL, and cleanup.
8. **Secrets** — references and lifecycle operations backed by Secrets Manager.
9. **DNS** — provider-neutral contract with Route 53 and Cloudflare adapters.
10. **Observability** — links and status aggregation for Prometheus, Grafana,
    and Loki.
11. **Cost estimation** — versioned assumptions and pre-provision estimates.
12. **Audit** — append-only records of requests, approvals, changes, retries,
    and destructive operations.

## 5. Repository strategy

Start as a monorepo so contracts and the first vertical slice can evolve
together:

```text
platform-idp/
  cmd/
    idp-api/
    idp-bootstrap/
  internal/
    auth/
    catalogue/
    provisioning/
    reconciliation/
    github/
    gitops/
    environments/
    secrets/
    dns/
    observability/
    costs/
    audit/
  portal/
  templates/
    go-chi-api/
    node-nest-api/
    java-spring-api/
    nextjs-web/
  charts/
    service/
    platform/
  infrastructure/
    bootstrap/
    modules/
    environments/
  config-schema/
  docs/
```

Module boundaries are logical, not separate deployables. Begin with one Go
control-plane deployment and split services only when operational evidence
justifies it.

## 6. Delivery plan

### Phase 0 — Contracts and threat model

Define the stable contracts before provisioning real infrastructure:

- Service request and environment configuration schemas.
- Golden-path template contract.
- Shared Helm values schema.
- Git repository and pull-request state transitions.
- Approval rules and role matrix.
- Resource naming, tagging, and ownership rules.
- Reconciliation and idempotency keys.
- Secret boundaries and threat model.
- Audit event schema.
- Preview lifecycle and TTL semantics.
- Cost-estimate assumptions and disclaimer.

Exit criteria:

- Example Go service configuration validates against the schema.
- A documented state machine covers request, plan, approval, apply, deploy,
  failure, retry, drift, and teardown.
- The threat model confirms no long-lived AWS keys or plaintext secrets are
  required.

### Phase 1 — AWS foundation bootstrap

Build the operator CLI and foundational OpenTofu modules:

- Interactive preflight checks and configuration capture.
- VPC, subnets, routing, EKS, node groups, ECR, S3 state, DynamoDB locking, and
  IAM/OIDC roles.
- Installation of Argo CD, External Secrets Operator, ingress, ExternalDNS,
  cert-manager, Prometheus, Grafana, and Loki.
- Shared RDS PostgreSQL and Secrets Manager integration.
- Route 53 adapter first, followed by Cloudflare through the same interface.
- Idempotent reruns and actionable failure output.

Exit criteria:

- A new AWS account can be bootstrapped from documented prerequisites.
- Rerunning bootstrap produces no unintended changes.
- The operator receives a working HTTPS portal URL.
- No secret values are committed to Git or stored in generated plaintext files.

### Phase 2 — Go golden-path vertical slice

Deliver the smallest complete developer journey:

- GitHub App and OAuth setup.
- Organization and team restriction.
- Portal service-request form.
- Go/chi template and repository creation.
- Central platform-config pull request.
- OpenTofu plan and pre-provision cost estimate.
- Two-person approval enforcement.
- Shared PostgreSQL database and credential creation.
- CI build/test and ECR publication.
- Shared Helm chart and Argo CD deployment.
- DNS, TLS, logs, metrics, and catalogue status.
- Reconciliation, retry, drift reporting, and audit trail.

Exit criteria:

- A developer can request a Go API with PostgreSQL and receive a healthy HTTPS
  staging endpoint without manually editing infrastructure or Kubernetes code.
- Both required approvals are enforced.
- A failed apply or deployment can be retried safely.
- Portal status agrees with Git, AWS, and Argo CD observed state.

### Phase 3 — Preview lifecycle

- GitHub pull-request webhook handling.
- Per-PR namespaces and values.
- Temporary database creation.
- Preview DNS and TLS.
- Update-on-push behavior.
- Close/merge teardown.
- TTL controller and orphan reconciliation.
- Quotas to prevent preview cost explosions.

Exit criteria:

- Opening a Go application pull request produces a working preview URL.
- Updating it refreshes the same environment.
- Closing it removes Kubernetes, DNS, secret, and database resources.
- TTL cleanup is idempotent and handles missed GitHub events.

### Phase 4 — Complete the v1 golden paths

Add and validate:

- NestJS API.
- Spring Boot API.
- Next.js web application.
- Production-ready multi-stage Dockerfiles for all templates.
- Consistent health, metrics, logs, CI, configuration, and secrets behavior.
- Template version metadata and a documented upgrade strategy.

Exit criteria:

- Every golden path passes the same conformance suite.
- Every generated repository reaches a healthy staging endpoint.
- Preview environments work consistently for all four templates.

### Phase 5 — Operational hardening and open-source release

- Protected teardown with snapshot and retention workflow.
- Backup and restore test for platform metadata and shared RDS.
- Least-privilege IAM review.
- Network-policy and pod-security defaults.
- Webhook signature verification and replay protection.
- Rate limits, quotas, and failure backoff.
- Upgrade, rollback, disaster-recovery, and uninstall documentation.
- Architecture, contribution, security-reporting, and local-development docs.
- License selection and release packaging.
- End-to-end tests against an ephemeral or dedicated AWS test account.

Exit criteria:

- A second organization can install the platform using only published docs.
- Backup restoration and platform upgrades are demonstrated.
- Teardown cannot bypass approvals or retention controls.
- The core journey has automated end-to-end coverage.

## 7. Testing strategy

- Unit tests for schemas, policy, cost calculations, state transitions, and
  reconcilers.
- Contract tests for GitHub, DNS, AWS, Argo CD, and template interfaces.
- Golden tests for rendered repositories and platform-config changes.
- Helm schema and rendering tests.
- OpenTofu formatting, validation, linting, and policy tests.
- Template conformance tests shared by all four golden paths.
- Integration tests using disposable namespaces and test databases.
- End-to-end tests covering bootstrap, provisioning, preview, retry, drift, and
  protected teardown.
- Failure-injection tests for missed webhooks, partial applies, unavailable
  providers, and abandoned previews.

## 8. v1 non-goals

- Multi-account AWS architecture.
- Multiple cloud providers.
- Generic existing-repository onboarding.
- Production workloads.
- Arbitrary Kubernetes or Helm customization by application teams.
- Dedicated RDS instances per service.
- Actual spend allocation or billing reconciliation.
- Distributed tracing.
- Service dependency graphs or API catalogues.
- Developer CLI.
- AI-generated infrastructure or AI configuration explanations.
- HashiCorp Vault.
- GitLab, Bitbucket, generic OIDC, Okta, or other source-control/identity
  providers.

## 9. Roadmap after v1

1. Production environments with promotion policies, stronger isolation,
   backups, recovery objectives, and deployment safeguards.
2. Multi-account AWS topology with platform, staging, and production accounts.
3. AI-assisted, read-only explanations of configuration, infrastructure plans,
   cost, drift, and rightsizing opportunities. The platform remains fully
   usable without AI, and AI never applies changes silently.
4. Existing-repository onboarding and conformance assessment.
5. Developer CLI backed by the same control-plane API.
6. Actual per-service cost reporting and budget alerts.
7. OpenTelemetry tracing with Tempo.
8. Service dependency and API modelling.
9. Dedicated databases and stronger tenant isolation options.
10. Generic OIDC and additional Git providers.

## 10. v1 product acceptance test

A fresh small engineering organization can:

1. Run the guided bootstrap CLI against one AWS account.
2. Supply its own GitHub App/OAuth, AWS, and Route 53 or Cloudflare
   configuration.
3. Sign into the HTTPS portal with GitHub.
4. Request a Go, NestJS, Spring Boot, or Next.js service, optionally with
   PostgreSQL where supported.
5. Review a clear resource plan and pre-provision monthly estimate.
6. Obtain independent engineering-owner and platform-owner approvals.
7. Merge and receive a healthy staging service with CI/CD, secrets, DNS, logs,
   metrics, and catalogue visibility.
8. Open an application pull request and receive an automatically managed
   preview environment.
9. Observe and safely retry failures or reconcile drift.
10. Tear a permanent service down through a protected, auditable process.

That journey is the boundary of v1. A capability is not complete merely because
its component is installed; it is complete when it participates reliably in
this end-to-end workflow.
