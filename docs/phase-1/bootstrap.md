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
DynamoDB locking. The foundation provisions VPC networking, EKS managed nodes,
shared PostgreSQL, and its master credential in Secrets Manager. The add-ons
state is applied only after that foundation is healthy.

After the foundation apply completes, install the delivery add-ons in their own
state:

```bash
tofu -chdir=infrastructure/addons init -backend-config=../../.idp/generated/addons-backend.hcl
tofu -chdir=infrastructure/addons plan -var-file=../../.idp/generated/addons.auto.tfvars.json
tofu -chdir=infrastructure/addons apply -var-file=../../.idp/generated/addons.auto.tfvars.json
```

This installs ingress-nginx, cert-manager with a Let's Encrypt HTTP-01 issuer,
External Secrets Operator, ExternalDNS, and Argo CD. Route 53 uses a dedicated
IRSA role limited to the configured zone. External Secrets can read only this
installation's `platform/<installation_id>/*` Secrets Manager prefix.

On a fresh machine, configure cluster access before any `kubectl` command:

```bash
aws eks update-kubeconfig --name <cluster_name_output> --region <aws_region> --profile <aws_profile>
```

For Cloudflare, create the namespace and token Secret before applying:

```bash
kubectl create namespace external-dns --dry-run=client -o yaml | kubectl apply -f -
read -rsp "Cloudflare API token: " CLOUDFLARE_API_TOKEN
printf %s "$CLOUDFLARE_API_TOKEN" | kubectl -n external-dns create secret generic cloudflare-api-token --from-file=api-token=/dev/stdin --dry-run=client -o yaml | kubectl apply -f -
unset CLOUDFLARE_API_TOKEN
```

Set `dns.cloudflareTokenSecretName` to `cloudflare-api-token`. These commands
are safe to rerun, and the token value does not enter shell history, OpenTofu
state, or generated configuration.
The token should have DNS-edit access only to the configured zone. A guided
secret-input command and Cloudflare API zone/token preflight remain open work
for a later bootstrap CLI slice. Until then, Cloudflare configuration is only
structurally validated; Route 53 receives the stronger live preflight check.

Prometheus, Grafana, Loki, and their storage/retention configuration are not in
this add-ons slice.

The Argo CD output is an expected URL, not a readiness assertion. After apply,
inspect `kubectl -n argocd get deployments,ingress,certificate`, wait for all
Argo deployments to finish rolling out, confirm the Ingress has an address,
confirm the Certificate is Ready, and test the HTTPS URL. Retrieve the initial password with
`argocd admin initial-password -n argocd`; rotate it on first login and delete
the initial-admin Secret afterward. The platform portal is not installed by
this slice, so Phase 1's portal-URL exit criterion remains open.

The pinned EKS module is 21.24.2 and requires AWS provider 6.52 or newer. The
VPC module is pinned to 6.6.1. Revisit pins deliberately and validate their
upgrade notes before changing them.
