# Phase 1 Bootstrap Slice

This slice validates operator inputs and renders non-secret OpenTofu inputs. It
does not run `tofu apply` automatically.

Replace the example AWS account, profile, DNS zone, domain, Kubernetes version,
and documentation-only operator CIDR before use. The EKS API rejects a global
`0.0.0.0/0` access range.

```bash
go run ./cmd/idp-bootstrap validate --config examples/bootstrap/config.json
go run ./cmd/idp-bootstrap preflight --config examples/bootstrap/config.json
go run ./cmd/idp-bootstrap render --config examples/bootstrap/config.json --output .idp/generated
```

The operator must already have authenticated AWS CLI and GitHub CLI sessions.
AWS credentials are never written to generated files. Review the generated
inputs, then provision state storage before initializing the foundation:

```bash
tofu -chdir=infrastructure/bootstrap-state init
tofu -chdir=infrastructure/bootstrap-state plan -var-file=../../.idp/generated/bootstrap-state.auto.tfvars.json
tofu -chdir=infrastructure/bootstrap-state apply -var-file=../../.idp/generated/bootstrap-state.auto.tfvars.json
tofu -chdir=infrastructure/foundation init -backend-config=../../.idp/generated/backend.hcl
tofu -chdir=infrastructure/foundation plan -var-file=../../.idp/generated/foundation.auto.tfvars.json
```

The state root intentionally uses local state for its first run; store that
small bootstrap state securely. The foundation uses encrypted/versioned S3 and
DynamoDB locking. The current foundation provisions VPC networking, EKS managed
nodes, shared PostgreSQL, and its master credential in Secrets Manager. Cluster
add-ons, DNS adapters, and the portal installation remain subsequent Phase 1
slices.

The pinned EKS module is 21.24.2 and requires AWS provider 6.52 or newer. The
VPC module is pinned to 6.6.1. Revisit pins deliberately and validate their
upgrade notes before changing them.
