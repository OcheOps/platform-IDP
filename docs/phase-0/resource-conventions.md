# Resource Conventions

## Canonical identity

- Service ID: lower-case DNS label, 2–40 characters.
- Environment ID: `staging` or `pr-<number>`.
- Canonical resource ID: `<installation-id>/<service-id>/<environment-id>`.
- Operation key: `<request-id>/<operation>/<desired-git-sha>`.

The stable service ID is never inferred from a mutable display name. Renaming a
service is a later migration feature, not an in-place string replacement.

## Naming

Names must include a deterministic 8-character hash of the canonical resource
ID when provider limits or global uniqueness make the plain name unsafe.

| Resource | Pattern |
|---|---|
| Kubernetes staging namespace | `svc-<service>` |
| Kubernetes preview namespace | `pr-<number>-<service>-<hash>` |
| ECR repository | `platform/<service>` |
| Database | `svc_<normalized-service>_<hash>` |
| Database user | `usr_<normalized-service>_<environment-hash>` |
| Secrets Manager prefix | `platform/<service>/<environment>/` |
| S3/OpenTofu state key | `services/<service>/<environment>/terraform.tfstate` |
| Staging DNS | `<service>.staging.<domain>` |
| Preview DNS | `pr-<number>.<service>.preview.<domain>` |

Names are calculated by one shared library. Truncation preserves the hash.

## Required AWS tags

| Tag | Meaning |
|---|---|
| `platform-idp:managed` | Always `true` for managed resources |
| `platform-idp:installation` | Stable installation ID |
| `platform-idp:service` | Stable service ID or `shared` |
| `platform-idp:owner` | GitHub team slug |
| `platform-idp:environment` | `platform`, `staging`, or `preview` |
| `platform-idp:request-id` | Request that last changed the resource |
| `platform-idp:expires-at` | RFC3339 expiry for disposable resources |
| `platform-idp:cost-scope` | `shared` or `incremental` |

All destructive discovery requires the installation tag and expected canonical
ID. Names or broad tag queries alone are insufficient authorization to delete.

## Idempotency

- Repository creation records the GitHub repository ID before subsequent work.
- AWS creation uses deterministic client/request tokens where supported.
- Database creation is transactionally recorded and verifies ownership markers
  before reuse or deletion.
- DNS changes upsert the exact managed record and never replace unrelated zone
  entries.
- Kubernetes applies are server-side and carry canonical identity labels.
- Audit events use a unique `(operation key, action, outcome)` constraint where
  duplicate delivery would otherwise create duplicate terminal events.
