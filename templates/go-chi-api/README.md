# Example Go API

Generated from `go-chi-api` template `v0.1.0`.

## Local development

```bash
go mod download
make test
make lint
make run
```

The service listens on port `8080`. `DATABASE_URL` is optional for the server;
when present, readiness verifies PostgreSQL connectivity. Database-enabled
deployments run migrations explicitly before rollout:

```bash
DATABASE_URL='postgres://...' make migrate
```

The application exposes `/health/live`, `/health/ready`, and `/metrics`. Logs
are JSON and include a generated or propagated `X-Request-ID`. Request headers,
query strings, database URLs, and secret values are never logged.
The JSON logger always redacts common sensitive keys; add application-specific
keys with the comma-separated `SENSITIVE_LOG_FIELDS` environment variable.

CI uses GitHub OIDC to assume `vars.AWS_ROLE_ARN`; repositories contain no AWS
keys. Images are tagged by commit SHA, pushed to `vars.ECR_REPOSITORY`, and
reported by immutable registry digest for GitOps promotion.
