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

The live adapter signs RS256 GitHub App JWTs and exchanges them for cached,
short-lived installation tokens. It uses the versioned REST API and requests
only Administration (write), Contents (write), and Members (read) permissions.
It never accepts a personal access token.

`cmd/repository-worker` loads the GitHub organization from `.idp/config.json`.
Operators provide `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, and
`GITHUB_APP_PRIVATE_KEY_FILE`; the key file must be a regular owner-only file
and its contents are never logged. The GitHub App must be installed for all
organization repositories so its installation token can access repositories
created after the token is minted. `GENERATED_DIRECTORY`, `INSTALLATION_CONFIG`,
and `GITHUB_API_URL` are optional overrides.
