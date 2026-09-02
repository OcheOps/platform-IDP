# Platform IDP

Platform IDP is a self-hosted internal developer platform for small engineering teams. It turns a request such as:

> Give me a Go API with PostgreSQL in staging.

into a reviewed, reproducible delivery workflow: a generated GitHub repository, an infrastructure change, an OpenTofu plan, independent approvals, AWS provisioning, and an Argo CD deployment.

This is an opinionated golden-path platform, not a general Kubernetes deployment UI. Git is the source of truth; the platform reconciles GitHub, OpenTofu, AWS, Kubernetes, and Argo CD into one service catalogue.

## Current status

The repository contains a working Phase 0 foundation and the first Phase 2 provisioning slices.

### Implemented

- Go control-plane components and a local durable reconciliation store.
- Guided, non-secret bootstrap CLI for one AWS account and region.
- OpenTofu foundation modules for VPC, EKS, managed PostgreSQL, encrypted S3 state, DynamoDB locking, and IAM/OIDC prerequisites.
- Add-ons configuration for Argo CD, External Secrets, ExternalDNS, cert-manager, Prometheus, Grafana, and Loki.
- Go/chi golden-path template with migrations, health checks, metrics, structured logging, tests, and a production Dockerfile.
- Service-request validation, versioned cost estimates, and audit events.
- GitHub App integration with least-privilege installation-token checks.
- Idempotent application-repository creation and central `platform-config` pull requests.
- Pinned GitHub Actions OpenTofu planning workflow with plan-digest status.
- Independent engineering-owner and platform-owner approval enforcement: requester exclusion, exact-head binding, live team membership checks, and two distinct approvers.
- Protected `platform-config` main branch policy requiring build, `platform/approvals`, and two reviews.
- Merge-triggered OpenTofu apply workflow that compares a fresh plan with the approved digest before mutation, publishes an apply attestation, and safely recovers an interrupted status publication.
- Apply reconciliation with durable state transitions, deterministic resource identities, safe retries, and operation-key audit deduplication.
- GitOps value rendering for digest-pinned staging releases and a constrained Argo CD ApplicationSet contract.
- A deployment worker that waits for the tested ECR image digest, creates a deterministic GitOps pull request, and records `awaiting_merge` until it is merged.
- Authenticated Argo CD health observation; a runtime reconciler marks requests ready only after the merged revision is synced and healthy.

### Deliberately not finished yet

- ECR lifecycle management and the shared deployment chart wired into generated GitOps values.
- DNS/TLS records, External Secrets database credentials, logs, metrics, and catalogue health links for each service.
- Pull-request preview environments, TTL cleanup, and temporary databases.
- Node/NestJS, Spring Boot, and Next.js golden paths.
- Portal UI, authentication/OAuth, catalogue read models, and SSE progress.
- Per-service OpenTofu state isolation and reading real OpenTofu outputs/AWS bindings instead of only deterministic identities.
- Protected teardown with snapshots and retention windows.
- Multi-account AWS support, tracing, actual spend reporting, and production.

The detailed product and delivery plan is in [`PLAN.md`](PLAN.md). Normative contracts live under [`docs/phase-0`](docs/phase-0), and current repository-worker behavior is documented in [`docs/phase-2/repository-worker.md`](docs/phase-2/repository-worker.md).

## Architecture

```text
Developer
   │ web portal (planned) / API
   ▼
Service request + cost estimate
   │
   ├── generated application repository
   └── platform-config pull request
          │ GitHub Actions: validate → plan → digest
          ▼
   engineering owner + platform owner approval
          │ protected merge
          ▼
   OpenTofu apply → AWS identities → GitOps desired state
          │
          ▼
   Argo CD → EKS → service, PostgreSQL, DNS, secrets, metrics and logs
```

The v1 deployment target is one self-hosted AWS account containing one EKS cluster. Staging is the first environment; pull-request previews are planned as disposable namespaces with a configurable TTL. Production is intentionally out of scope for v1.

## Repository layout

- `cmd/idp-bootstrap` — operator installation CLI.
- `cmd/platform-api` — service-request API.
- `cmd/repository-worker` — repository, config-PR, plan, approval, apply, GitOps deployment, and Argo runtime reconcilers.
- `internal/*` — contracts and provider adapters.
- `infrastructure/` — OpenTofu foundation and add-ons modules.
- `charts/service` — shared service Helm chart.
- `templates/go-chi-api` — first golden path.
- `contracts/` — JSON schemas and contract tests.
- `docs/` — phase specifications, security model, workflow, and conventions.

## Local development

Requirements: Go, OpenTofu, Terraform-compatible AWS credentials/profile, `kubectl` for postflight checks, `gh`, and Python 3 for the generated validator. No secret values belong in Git or generated configuration files.

Run the test and static checks:

```bash
GOCACHE=/tmp/platform-idp-cache GOPROXY=off go test -race ./...
GOCACHE=/tmp/platform-idp-cache GOPROXY=off go vet ./...
```

### Bootstrap an installation

The CLI follows a guided, reviewable flow. It writes only durable, non-secret configuration and generated OpenTofu inputs.

```bash
go run ./cmd/idp-bootstrap configure --config .idp/config.json
go run ./cmd/idp-bootstrap validate --config .idp/config.json
go run ./cmd/idp-bootstrap preflight --config .idp/config.json
go run ./cmd/idp-bootstrap render --config .idp/config.json --output .idp/generated
```

Plan and apply each installation stage separately. Applying requires the full digest printed by the plan command:

```bash
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage bootstrap-state
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage foundation
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage addons
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage addons --apply --approve <reviewed-plan-digest>
go run ./cmd/idp-bootstrap postflight --config .idp/config.json
```

The bootstrap configuration includes AWS account/region, GitHub organization, distinct `engineeringOwnerTeam` and `platformOwnerTeam` slugs, DNS provider (`route53` or `cloudflare`), hosted-zone details, and restricted cluster access CIDRs. The GitHub App private key is supplied separately through a protected file; it is never written to `.idp/config.json`.

### Configure the GitHub App and repository worker

```bash
./scripts/setup-github-app.sh
./scripts/run-repository-worker.sh --preflight
./scripts/run-repository-worker.sh
```

The App is private to the organization and installed for all organization repositories so it can manage repositories created later. Its effective permissions are checked before work begins. The environment file accepts the five required settings written by the wizard plus optional `INSTALLATION_CONFIG`, `GENERATED_DIRECTORY`, `PLATFORM_CONFIG_REPOSITORY`, and `GITHUB_ACTIONS_ROLE_ARN` overrides.

## Security model

- AWS authentication uses GitHub Actions OIDC; long-lived AWS keys are not required.
- GitHub App tokens are short-lived and installation-scoped.
- Secrets live in AWS Secrets Manager and are synchronized by External Secrets; secret values are never committed to Git.
- Staging provisioning requires one current engineering owner and one current platform owner. The requester cannot approve, and one person cannot fill both roles.
- Reviews and plans are bound to the exact PR head SHA and immutable plan digest. Changed desired state requires a new plan and approvals.
- The platform-config branch ruleset blocks bypasses and requires the build and approval statuses plus two reviews.
- AWS resources use deterministic names and managed/installation/service/environment/request tags to make reconciliation and cleanup auditable.

## Contributing

Keep changes inside the documented contracts and golden paths. Add unit, contract, or generated-artifact tests with each behavior change. Run the full race-enabled test suite, `go vet`, shell checks, and `git diff --check` before opening a pull request. Do not commit credentials, private keys, Terraform state, plan files, or secret values.

The project is currently pre-release. Architecture and scope decisions should be recorded in `docs/` and reflected in `PLAN.md` before adding a new provider, environment, or arbitrary Kubernetes escape hatch.
