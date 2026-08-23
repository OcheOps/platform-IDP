# Provisioning Workflow

## Source-of-truth rule

Git is authoritative for requested and approved desired state. OpenTofu state is
authoritative for provider bindings. AWS, Kubernetes, and Argo CD are
authoritative for observed runtime state. The portal aggregates these sources
and must label stale or unavailable observations; it must not invent success.

## State machine

| State | Entered when | Permitted next states |
|---|---|---|
| `draft` | Portal accepts a valid request | `planning`, `cancelled` |
| `planning` | Config PR exists and plan checks start | `awaiting_approval`, `plan_failed`, `cancelled` |
| `plan_failed` | Validation, policy, estimate, or OpenTofu plan fails | `planning`, `cancelled` |
| `awaiting_approval` | A current plan and estimate are posted | `approved`, `planning`, `cancelled` |
| `approved` | Independent engineering and platform approvals exist for the current head SHA | `applying`, `awaiting_approval` |
| `applying` | Approved PR is merged and apply begins | `deploying`, `apply_failed` |
| `apply_failed` | OpenTofu apply fails or is interrupted | `applying`, `teardown_pending` |
| `deploying` | Infrastructure is ready and Argo CD observes desired state | `ready`, `deploy_failed` |
| `deploy_failed` | Argo sync or health deadline fails | `deploying`, `teardown_pending` |
| `ready` | Desired revision is synced and healthy | `drifted`, `planning`, `teardown_pending` |
| `drifted` | Declared and observed state differ | `ready`, `planning`, `teardown_pending` |
| `teardown_pending` | Protected teardown PR is opened | `teardown_approved`, `ready` |
| `teardown_approved` | Both current-head approvals exist | `retaining`, `teardown_failed` |
| `retaining` | Final snapshot completes and retention timer starts | `tearing_down`, `teardown_pending` |
| `tearing_down` | Retention expires and destroy begins | `deleted`, `teardown_failed` |
| `teardown_failed` | Snapshot or destroy fails | `teardown_approved`, `tearing_down` |
| `deleted` | Declared resources are absent and outputs are archived | terminal |
| `cancelled` | An unmerged request is closed | terminal |

## Generated repository lifecycle

Repository creation is a child workflow correlated to the service request:

| State | Entered when | Permitted next states |
|---|---|---|
| `repository_pending` | Request validation succeeds | `repository_rendering`, `cancelled` |
| `repository_rendering` | A pinned template version is rendered | `repository_creating`, `repository_failed` |
| `repository_creating` | Rendered output and settings pass validation | `repository_ready`, `repository_failed` |
| `repository_failed` | Render, GitHub creation, protection, or initial push fails | `repository_rendering`, `repository_creating`, `cancelled` |
| `repository_ready` | Repository ID is recorded, initial commit exists, and protection/settings match policy | `planning` |

The workflow records the GitHub repository ID as soon as creation succeeds.
Retries first look up that ID and canonical name, then repair missing settings or
content instead of creating another repository. A repository created by a
failed request is retained in a quarantined/private state until retry or an
explicit audited cleanup; it is never silently deleted.

## Approval invariants

- Approvals bind to the pull request head SHA and plan digest.
- Any desired-state or generated-plan change invalidates prior approvals.
- One approver must currently hold engineering-owner authority for the service.
- One different approver must currently hold platform-owner authority.
- The requester cannot satisfy either required approval.
- Branch protection blocks merge until checks and both approvals succeed.

## Reconciliation

The control plane reconciles by `(resource kind, canonical resource ID)` and:

1. reads desired Git revision;
2. reads the latest successful OpenTofu outputs;
3. observes AWS, Argo CD, and Kubernetes;
4. calculates a typed difference;
5. records drift without automatically expanding privileges or capacity;
6. performs only the action allowed for the current workflow state;
7. records an audit event and schedules the next observation.

Retries use exponential backoff with jitter and a bounded attempt window.
Permanent policy or validation failures wait for a new Git revision. Transient
provider failures retry. A retry reuses the same operation key and must converge
without creating duplicate repositories, databases, DNS records, or namespaces.

## Drift policy

- Benign observation lag is reported as `reconciling`, not drift.
- Git-to-runtime differences surviving the configured grace period are
  `drifted`.
- Reducing unexpected excess capacity may be proposed automatically by PR but
  not applied silently.
- Missing security controls, public exposure, or broadened IAM is high-severity
  drift and alerts platform owners.
- The platform never imports arbitrary manual infrastructure into desired state.

## Failure reporting

Each failure exposes a stable error code, affected component, safe user-facing
message, correlation ID, retryability, first/last occurrence, and a link to
redacted logs. Provider output must be scrubbed before display or persistence.
