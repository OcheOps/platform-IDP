# Repository worker orchestration

The repository worker consumes local-development workflow records in
`repository_creating` and retries prior `github_*` failures. One reconciliation:

1. creates or finds the canonical private repository;
2. persists the numeric GitHub repository ID immediately;
3. ensures exactly one initial rendered commit;
4. ensures team access and repository policy;
5. independently verifies identity, content, and settings;
6. transitions to `repository_ready` and records `repository.created`.

Each GitHub operation is an idempotent interface. A failure records a stable,
safe `github_*` code and a `repository.failed` audit event while retaining any
repository ID already learned. The worker never deletes a partially configured
repository.
Before the first provider call, the single-process development store records the attempt and
records `repository.started`. Failures persist an exponential next-attempt time;
automatic reconciliation stops after eight attempts for operator review.
Only errors explicitly classified as transient are retried; permission, policy,
and validation failures wait for operator or desired-state changes.

This slice implements and tests orchestration plus the development filesystem
store. The live GitHub App adapter is the next trust-boundary slice: it will mint
short-lived installation tokens, use the versioned REST API, and require only
Administration, Contents, and Members permissions needed for creation, initial
content, rulesets, and owner-team access. No personal access token is part of
the design.
