# Pre-Provision Cost Model

The estimate is advisory, not a quote or billing record. AWS prices, taxes,
discounts, free tiers, credits, data transfer, usage, exchange rates, and shared
utilization can make actual charges differ. The portal displays the pricing
region, currency, calculation time, model version, assumptions, and confidence
notes beside every estimate.

## Presentation

Every plan separates:

1. **Shared platform baseline** — EKS control plane, base worker capacity,
   shared RDS baseline, ingress, NAT/networking, and observability.
2. **Incremental monthly service estimate** — requested compute reservation,
   allocated database/storage allowance, incremental load-balancer or storage
   resources, Secrets Manager entries, DNS, and expected logs/metrics.
3. **Preview scenario** — estimated hours per preview multiplied by configured
   concurrency, plus temporary compute, database allocation, ingress, and
   telemetry.

Shared costs are shown for context but are not counted again as fully
incremental service cost. Allocation assumptions are visible.

## Versioned inputs

- AWS region and currency.
- Hours per month: 730.
- Resource quantities from the reviewed desired state.
- Current cached AWS public on-demand prices and retrieval timestamp.
- Shared-capacity allocation method and utilization assumption.
- Preview TTL, expected concurrency, and active-hours assumption.
- A fallback price table version when the Pricing API is unavailable.

The estimate digest includes desired configuration, normalized prices, and model
version. Any of those changing invalidates approvals and requires a new plan.
Unavailable or stale pricing is prominent and can be configured to block merge.

## Rightsizing guardrails

V1 offers approved capacity tiers rather than arbitrary quantities. It flags
high replica counts, memory/CPU imbalance, previews above quota, and requests
whose incremental estimate exceeds the installation's review threshold. It
does not claim utilization-based savings before actual metrics exist.
