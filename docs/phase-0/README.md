# Phase 0: Contracts and Threat Model

Phase 0 fixes the boundaries that the portal, control plane, golden paths,
OpenTofu, GitHub workflows, and shared Helm chart will implement.

## Normative artifacts

- [`contracts/schemas/common.schema.json`](../../contracts/schemas/common.schema.json)
  is the canonical source for service identity, template, image, resource, and
  approved replica definitions shared by the other schemas.
- [`contracts/schemas/service-request.schema.json`](../../contracts/schemas/service-request.schema.json)
  defines the developer's staging request.
- [`contracts/schemas/environment.schema.json`](../../contracts/schemas/environment.schema.json)
  defines Git-backed desired state for staging and previews.
- [`contracts/schemas/helm-values.schema.json`](../../contracts/schemas/helm-values.schema.json)
  defines the safe deployment fields exposed by the shared chart.
- [`contracts/schemas/audit-event.schema.json`](../../contracts/schemas/audit-event.schema.json)
  defines append-only security and lifecycle events.
- [`workflow.md`](workflow.md) defines lifecycle transitions and retry behavior.
- [`authorization.md`](authorization.md) defines roles and approval policy.
- [`resource-conventions.md`](resource-conventions.md) defines identity, naming,
  ownership, tagging, and idempotency.
- [`golden-path-contract.md`](golden-path-contract.md) defines template
  conformance.
- [`preview-lifecycle.md`](preview-lifecycle.md) defines preview TTL semantics.
- [`cost-model.md`](cost-model.md) defines estimate assumptions and presentation.
- [`threat-model.md`](threat-model.md) defines assets, trust boundaries, threats,
  and required controls.

## Contract rules

1. `apiVersion` changes only for a breaking schema change.
2. Unknown fields are rejected at trust boundaries.
3. Git records desired infrastructure state; the portal database stores workflow
   metadata, audit events, and replaceable read models.
4. Mutable image tags are not accepted in desired state; deployments use image
   digests.
5. Secret values are never accepted by these contracts. Only remote references
   are allowed.
6. Provider-generated identifiers and observed state do not get committed back
   into a request. They are stored as outputs and projected into the catalogue.
7. All mutations carry a stable `requestId` used for idempotency and audit
   correlation.

## Validation

Run:

```bash
go test ./...
```

The test suite compiles every referenced schema and proves that the example Go
service request and staging environment conform. It also proves that capacity
outside the approved v1 bounds is rejected.

## Phase 0 exit criteria

- [x] Example Go service configuration validates against versioned schemas.
- [x] Request, plan, approval, apply, deploy, failure, retry, drift, and teardown
      states are documented.
- [x] Golden-path and shared Helm contracts are defined.
- [x] Roles and independent approval rules are defined.
- [x] Naming, tags, ownership, and idempotency rules are defined.
- [x] Preview TTL and orphan behavior are defined.
- [x] Audit events are machine-readable.
- [x] Estimate assumptions and disclaimer are defined.
- [x] The threat model prohibits long-lived AWS keys and plaintext secrets.
