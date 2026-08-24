# Phase 1 Bootstrap Slice

This slice validates operator inputs and renders non-secret OpenTofu inputs. It
does not run `tofu apply` automatically.

Create a local configuration with the guided command:

```bash
go run ./cmd/idp-bootstrap configure --config .idp/config.json
```

The wizard asks only for durable configuration. It never asks for AWS or GitHub
credentials or the value of a Cloudflare API token. For Cloudflare it records
only the name of the Kubernetes Secret that will hold the token. The config is
created with owner-only permissions and an existing file is never overwritten.

Alternatively, copy and edit the example. Replace its AWS account, profile, DNS
zone, domain, Kubernetes version, and documentation-only operator CIDR before
use. The EKS API rejects a global `0.0.0.0/0` access range.

```bash
go run ./cmd/idp-bootstrap validate --config .idp/config.json
go run ./cmd/idp-bootstrap preflight --config .idp/config.json
go run ./cmd/idp-bootstrap render --config .idp/config.json --output .idp/generated
```

The operator must already have authenticated AWS CLI and GitHub CLI sessions.
AWS credentials are never written to generated files. Review the generated
inputs. The guided installer advances through three explicit stages. Plan each
stage, inspect the saved plan, then apply that exact plan by approving its
printed SHA-256 digest:

```bash
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage bootstrap-state
tofu -chdir=infrastructure/bootstrap-state show "$PWD/.idp/generated/bootstrap-state.tfplan"
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage bootstrap-state --apply --approve <full-plan-digest>

go run ./cmd/idp-bootstrap install --config .idp/config.json --stage foundation
tofu -chdir=infrastructure/foundation show "$PWD/.idp/generated/foundation.tfplan"
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage foundation --apply --approve <full-plan-digest>

go run ./cmd/idp-bootstrap install --config .idp/config.json --stage addons
tofu -chdir=infrastructure/addons show "$PWD/.idp/generated/addons.tfplan"
go run ./cmd/idp-bootstrap install --config .idp/config.json --stage addons --apply --approve <full-plan-digest>
go run ./cmd/idp-bootstrap postflight --config .idp/config.json
```

Planning never applies infrastructure. Applying requires both `--apply` and the
full digest of the reviewed plan; a changed or replaced plan is rejected. Saved
plans may contain sensitive provider data, so the installer makes them
owner-readable only inside the Git-ignored `.idp` directory and deletes each
plan after a successful apply. A failed apply retains its plan for inspection
and retry. Avoid copying plan files elsewhere or attaching them to issues.

The installer runs preflight checks without redisplaying raw provider output.
It stops at the first failed stage and provides recovery context. With unchanged
configuration, initialization, planning, and applies are safe to repeat. Backend
configuration changes require deliberate OpenTofu backend migration and are not
handled automatically.

The add-ons apply runs postflight automatically. `postflight` is also safe to
rerun independently when DNS or certificate propagation is still converging.
It waits for Ready EKS nodes and Ready pods for every installed release, then
checks the public Argo CD HTTPS endpoint. It does not redisplay raw command or
provider output on failure.

For Cloudflare, create the token Secret using the instructions below after EKS
exists. The installer verifies that Secret before it plans add-ons, so the first
run can intentionally stop there without losing completed foundation work.

The state root intentionally uses local state for its first run; store that
small bootstrap state securely. The foundation uses encrypted/versioned S3 and
DynamoDB locking. The foundation provisions VPC networking, EKS managed nodes,
shared PostgreSQL, and its master credential in Secrets Manager. The add-ons
state is applied only after that foundation is healthy.

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

The same add-ons apply installs Prometheus, Grafana, Loki, and Alloy. Prometheus
and Loki target seven-day retention. Prometheus begins size-based retention at
20 GB, while Loki has a hard 20 GiB local-volume ceiling. Loki ingestion is also
limited to 1 MB/s with a 2 MB burst. Alloy
parses CRI records and labels each stream with its namespace, pod, container,
and service (the namespace until the shared service chart supplies richer
metadata). Prometheus discovers `ServiceMonitor` and `PodMonitor` resources in
all namespaces so that chart can register generated services declaratively.
All components have explicit CPU and memory requests and limits suitable as a
small-cluster starting point; operators must rightsize them from observed load.

Grafana includes Loki as a data source but is cluster-internal in this slice.
Access it temporarily with:

```bash
kubectl -n observability port-forward service/kube-prometheus-stack-grafana 3000:80
```

Then open `http://localhost:3000`. Retrieve the generated admin password from
the chart-managed Kubernetes Secret; do not copy it into Git or OpenTofu
variables. Public ingress waits for GitHub-organization authentication rather
than exposing a password-only administrative UI.

This first observability path intentionally uses ephemeral storage. A pod
reschedule, node replacement, or chart recreation can discard metrics, logs,
Grafana settings, and dashboards not represented as code. Retention deletion is
asynchronous; only Loki's size-limited local volume provides a hard capacity
bound. Prometheus can temporarily exceed its 20 GB retention threshold through
its write-ahead log, memory-mapped files, and deletion headroom.
Do not use this setup for production or audit retention. EBS CSI, encrypted
persistent volumes, backup, tested recovery, and dashboards-as-code remain
Phase 1 hardening work.

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
