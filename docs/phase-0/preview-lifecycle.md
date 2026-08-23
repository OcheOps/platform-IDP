# Preview Lifecycle

## Identity and creation

A preview is uniquely identified by GitHub repository ID and pull-request
number. Pushes update that preview; they never create additional environments.
Creation begins only for pull requests from branches whose workflow is allowed
to access the configured GitHub environment. Forked pull requests receive no
secrets and do not provision previews in v1.

## TTL

- Default TTL: 72 hours from the last eligible pull-request update.
- Configurable range: 1–168 hours.
- An eligible push or explicit authorized extension advances `expiresAt`.
- The controller compares expiry using server time, not client input.
- Close or merge requests immediate cleanup; TTL is the missed-event safety net.
- A team quota limits concurrent previews and aggregate requested capacity.

## Teardown order

1. Mark the preview `terminating` so updates cannot race cleanup.
2. Disable ingress and remove DNS.
3. Stop Argo reconciliation and delete the application/namespace.
4. Delete External Secrets objects and the scoped Secrets Manager values.
5. Terminate database sessions, drop the temporary database and role.
6. Remove preview desired state and record terminal audit evidence.

Preview databases are disposable and are not snapshotted. Cleanup verifies the
canonical identity and managed tags at each step. A partial failure remains in
`terminating` and retries until every resource is absent. Orphan scans compare
GitHub open pull requests, desired state, Kubernetes namespaces, DNS records,
secrets, and database ownership markers.
