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
only Administration (write), Contents (write), Workflows (write), and Members
(read) permissions. Workflows write is required to initialize the pinned
planning workflow in the central configuration repository.
It never accepts a personal access token.

`cmd/repository-worker` loads the GitHub organization from `.idp/config.json`.
Operators provide `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, and
`GITHUB_APP_PRIVATE_KEY_FILE`; the key file must be a regular owner-only file
and its contents are never logged. The GitHub App must be installed for all
organization repositories so its installation token can access repositories
created after the token is minted. `GENERATED_DIRECTORY`, `INSTALLATION_CONFIG`,
and `GITHUB_API_URL` are optional overrides.

Run the repeatable operator wizard to register and install the App, secure its
downloaded private key, write the local runtime environment, and perform a
read-only credential/permission preflight:

```bash
./scripts/setup-github-app.sh
```

Then start the development worker without sourcing the environment file:

```bash
./scripts/run-repository-worker.sh
```

The wizard disables webhooks and requests only repository administration,
checks read, contents write, pull requests read, commit statuses read,
workflows write, and organization members read. It installs for all
organization repositories. That scope
lets the worker manage repositories created later, but also grants it access to
existing repositories, so a dedicated organization is recommended. The wizard
never writes the private key into an environment file and refuses symlinked or
broadly scoped key inputs. Its preflight verifies the organization, installation
scope, exact effective permissions, credentials, and token exchange without
creating a repository. Reruns can safely reuse an existing local configuration.

After an application repository becomes `repository_ready`, the same process
reconciles its central desired-state review. It creates or verifies the private
`platform-config` repository, writes the canonical request and immutable cost
estimate to `services/<service>/staging/` on a deterministic request branch,
and opens one pull request. Existing branches and pull requests are reused only
when their identities, head SHA, and committed bytes match; divergent content
is never overwritten. Once verified, the service request enters `planning`.
`PLATFORM_CONFIG_REPOSITORY` optionally overrides the repository name.

On first use, the worker initializes that repository with an installation-bound
S3/DynamoDB backend, a real OpenTofu service module for ECR and database-secret
containers, and a pull-request planning workflow. The workflow pins checkout,
AWS credential, and OpenTofu setup actions by commit; uses OpenTofu 1.12.6;
authenticates to AWS through the configured OIDC role; validates that a request
changes exactly one request/estimate pair; and deletes the potentially
sensitive binary plan after writing only its digest and resource-action counts
to the job summary. The generated module uses AWS provider 6.61.0.

The workflow publishes the SHA-256 digest of canonical `tofu show -json`
output as a commit status on the exact pull-request head. The worker accepts
that digest only when the GitHub Actions `build` check on the same head has
completed successfully. It then persists the head SHA and plan digest, appends
a `plan.completed` audit event, and moves the request to `awaiting_approval`.
Failed or mismatched checks enter `plan_failed`; absent or running checks remain
in `planning`.
