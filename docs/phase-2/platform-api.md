# Platform API service-request slice

The first platform API slice accepts the existing
`platform.idp/v1alpha1` `ServiceRequest` contract at
`POST /v1/service-requests`. It validates unknown fields and capacity through
the canonical JSON Schema, requires the authenticated actor to match
`metadata.requester`, renders the pinned Go golden path, and atomically stores
the complete validated request, actor, initial audit event, and workflow
metadata under the configured generated-data directory.

`requestId` is the idempotency key. Repeating identical content returns the
existing result; reusing the ID for different content returns `409 Conflict`.
Rendering ends in `repository_creating`, not `repository_ready`: the latter is
reserved for a GitHub repository with an initial commit and verified settings.
Request-scoped staging plus a persisted ownership marker lets retries recover
when a process stops between rendering and its final state update.
The generated repository is local input for the next GitHub App worker slice;
this API does not create or push a GitHub repository yet.

## Local development server

`cmd/platform-api` is intentionally a loopback-only adapter. It uses a
development bearer token and an `X-Platform-Actor` header so the HTTP workflow
can be exercised before GitHub OAuth/App authentication is available. It
refuses non-loopback addresses and must not be exposed as the production portal
API.

The adapter loads organization and AWS region from the guided installer's
canonical `.idp/config.json`. Required environment variables are
`PLATFORM_API_DEV_TOKEN` (at least 32 characters) and
`GITHUB_ACTIONS_ROLE_ARN`. Optional overrides are `INSTALLATION_CONFIG`,
`SCHEMA_DIRECTORY`, `GO_TEMPLATE_DIRECTORY`, and `GENERATED_DIRECTORY`.

The production API must replace this development authenticator with GitHub
organization membership and team authorization before any public deployment.
The filesystem metadata store is also intentionally single-process. It is a
recoverable local-development seam, not the horizontally scaled production
workflow database; production deployment waits for the transactional store and
append-only audit-event slice.
