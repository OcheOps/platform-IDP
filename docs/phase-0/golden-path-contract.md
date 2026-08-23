# Golden-Path Contract

Every versioned golden path contains:

- a manifest declaring ID, semantic version, language, runtime, default port,
  database capability, and required inputs;
- application source with `/health/live`, `/health/ready`, and `/metrics`;
- structured JSON logs to stdout/stderr with correlation IDs and no secrets;
- unit tests, lint command, and deterministic dependency lock file;
- a production multi-stage Dockerfile running as a non-root user;
- a GitHub Actions workflow that tests, scans, builds, and publishes an immutable
  image digest to ECR using OIDC;
- database migrations that are explicit, repeatable, and fail before rollout;
- documented local-development commands;
- generated platform metadata referencing the template version.

Templates do not contain AWS credentials, Kubernetes credentials, environment
secrets, copied deployment manifests, or mutable production image tags.

## Shared conformance checks

1. Repository renders with only declared inputs.
2. Build and tests pass from a clean checkout.
3. Container runs as non-root and responds on all three platform endpoints.
4. SIGTERM produces graceful shutdown within the platform deadline.
5. Logs are valid JSON and redact configured sensitive fields.
6. Database-enabled variants migrate and connect using `DATABASE_URL` only.
7. The shared Helm values generated for the service validate against the
   contract schema.
8. The same image digest can be promoted without rebuilding.

Template upgrades are explicit pull requests. The catalogue shows the current
and latest compatible versions; v1 does not silently rewrite repositories.
