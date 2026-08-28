package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/configworker"
)

func (client *Client) initializeConfigRepository(ctx context.Context, work configworker.Work) error {
	files, err := platformRepositoryFiles(work)
	if err != nil {
		return err
	}
	entries := make([]map[string]string, 0, len(files))
	for path, content := range files {
		entries = append(entries, map[string]string{"path": path, "mode": "100644", "type": "blob", "content": content})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/trees", map[string]any{"tree": entries}, &tree, http.StatusCreated); err != nil {
		return err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/commits", map[string]any{"message": "Initialize platform configuration", "tree": tree.SHA, "parents": []string{}}, &commit, http.StatusCreated); err != nil {
		return err
	}
	_, err = client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/refs", map[string]string{"ref": "refs/heads/main", "sha": commit.SHA}, nil, http.StatusCreated)
	return err
}

func platformRepositoryFiles(work configworker.Work) (map[string]string, error) {
	p := work.Platform
	for name, value := range map[string]string{"installation ID": p.InstallationID, "AWS account ID": p.AWSAccountID, "AWS region": p.AWSRegion, "AWS role ARN": p.AWSRoleARN, "state bucket": p.StateBucket, "lock table": p.LockTable, "base domain": p.BaseDomain} {
		if strings.TrimSpace(value) == "" {
			return nil, errors.New(name + " is required to initialize platform-config")
		}
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`).MatchString(p.InstallationID) || !regexp.MustCompile(`^[0-9]{12}$`).MatchString(p.AWSAccountID) || !regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-[0-9]$`).MatchString(p.AWSRegion) {
		return nil, errors.New("platform installation, account, or region is invalid")
	}
	expectedARN := regexp.MustCompile(`^arn:aws:iam::` + p.AWSAccountID + `:role/[A-Za-z0-9+=,.@_/-]{1,512}$`)
	if !expectedARN.MatchString(p.AWSRoleARN) || strings.ContainsAny(p.StateBucket+p.LockTable+p.BaseDomain, "\r\n\"'") {
		return nil, errors.New("platform role or backend values are invalid")
	}
	variables, _ := json.MarshalIndent(map[string]string{"installation_id": p.InstallationID, "aws_account_id": p.AWSAccountID, "aws_region": p.AWSRegion, "base_domain": p.BaseDomain}, "", "  ")
	backend := "bucket = \"" + p.StateBucket + "\"\nkey = \"services/terraform.tfstate\"\nregion = \"" + p.AWSRegion + "\"\ndynamodb_table = \"" + p.LockTable + "\"\nencrypt = true\n"
	return map[string]string{
		"README.md":         "# Platform configuration\n\nManaged by Platform IDP. Service requests are reviewed through pull requests; do not commit secrets.\n",
		".gitignore":        ".terraform/\n*.tfplan\n*.tfstate*\n",
		".opentofu-version": "1.12.6\n",
		"backend.hcl":       backend,
		"infrastructure/platform.auto.tfvars.json": string(variables) + "\n",
		"infrastructure/versions.tf":               platformVersionsTF,
		"infrastructure/main.tf":                   platformMainTF,
		"infrastructure/variables.tf":              platformVariablesTF,
		"scripts/validate.py":                      platformValidatePython,
		".github/workflows/plan.yaml":              strings.NewReplacer("__AWS_ROLE_ARN__", p.AWSRoleARN, "__AWS_REGION__", p.AWSRegion).Replace(platformPlanWorkflow),
	}, nil
}

const platformVersionsTF = `terraform {
  required_version = "= 1.12.6"
  backend "s3" {}
  required_providers {
    aws = { source = "hashicorp/aws", version = "= 6.61.0" }
  }
}
provider "aws" { region = var.aws_region }
`
const platformVariablesTF = `variable "installation_id" { type = string }
variable "aws_account_id" { type = string }
variable "aws_region" { type = string }
variable "base_domain" { type = string }
`
const platformMainTF = `locals {
  request_files = fileset("${path.module}/../services", "*/staging/request.json")
  requests      = { for path in local.request_files : jsondecode(file("${path.module}/../services/${path}")).metadata.name => jsondecode(file("${path.module}/../services/${path}")) }
  tags          = { "platform-idp:managed" = "true", "platform-idp:installation" = var.installation_id, "platform-idp:environment" = "staging", "platform-idp:cost-scope" = "incremental" }
}
resource "aws_ecr_repository" "service" {
  for_each             = local.requests
  name                 = "platform/${each.key}"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
  encryption_configuration { encryption_type = "AES256" }
  tags = merge(local.tags, { "platform-idp:service" = each.key, owner = each.value.metadata.owner })
}
resource "aws_secretsmanager_secret" "database" {
  for_each                = { for name, request in local.requests : name => request if request.spec.database.engine == "postgres" }
  name                    = "platform/${var.installation_id}/${each.key}/staging/database"
  recovery_window_in_days = 7
  tags                    = merge(local.tags, { "platform-idp:service" = each.key, owner = each.value.metadata.owner })
}
`
const platformValidatePython = `import json, pathlib, re, subprocess, sys
base, head = sys.argv[1], sys.argv[2]
changed = subprocess.check_output(["git", "diff", "--name-only", base, head], text=True).splitlines()
pattern = re.compile(r"services/([a-z][a-z0-9-]{0,38}[a-z0-9])/staging/(request|estimate)\.json")
matches = [pattern.fullmatch(path) for path in changed]
if len(changed) != 2 or any(match is None for match in matches) or len({match.group(1) for match in matches}) != 1 or {match.group(2) for match in matches} != {"request", "estimate"}:
    raise SystemExit("a provisioning PR must change exactly one request.json and estimate.json pair")
root = pathlib.Path("services") / matches[0].group(1) / "staging"
request, estimate = json.loads((root / "request.json").read_text()), json.loads((root / "estimate.json").read_text())
if request.get("kind") != "ServiceRequest" or request.get("metadata", {}).get("name") != matches[0].group(1) or request.get("spec", {}).get("environment") != "staging":
    raise SystemExit("request identity or environment is invalid")
if not re.fullmatch(r"sha256:[0-9a-f]{64}", estimate.get("digest", "")) or estimate.get("currency") != "USD":
    raise SystemExit("estimate provenance is invalid")
capacity = request["spec"]["capacity"]
if capacity["minReplicas"] > capacity["maxReplicas"] or capacity["maxReplicas"] > 10:
    raise SystemExit("capacity exceeds platform policy")
print(estimate["digest"])
`
const platformPlanWorkflow = `name: plan
on:
  pull_request:
    paths: ["services/**", "infrastructure/**", "backend.hcl"]
permissions:
  contents: read
  id-token: write
  statuses: write
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with: { fetch-depth: 0, persist-credentials: false, ref: ${{ github.event.pull_request.head.sha }} }
      - name: Validate request scope and policy
        run: python3 scripts/validate.py "${{ github.event.pull_request.base.sha }}" "${{ github.event.pull_request.head.sha }}"
      - uses: opentofu/setup-opentofu@a1320f892987e89d278cc92dc5adc984fb93aca4 # v2.0.2
        with: { tofu_version_file: .opentofu-version, tofu_wrapper: false }
      - uses: aws-actions/configure-aws-credentials@00943011d9042930efac3dcd3a170e4273319bc8 # v5.1.0
        with: { role-to-assume: __AWS_ROLE_ARN__, aws-region: __AWS_REGION__ }
      - name: OpenTofu format and initialize
        run: tofu -chdir=infrastructure fmt -check && tofu -chdir=infrastructure init -input=false -backend-config=../backend.hcl
      - name: Create reviewed plan
        env:
          HEAD_SHA: ${{ github.event.pull_request.head.sha }}
        run: |
          umask 077
          tofu -chdir=infrastructure plan -input=false -lock-timeout=5m -out="$RUNNER_TEMP/service.tfplan"
		  tofu -chdir=infrastructure show -json "$RUNNER_TEMP/service.tfplan" | jq -S -c . > "$RUNNER_TEMP/service-plan.json"
		  digest="$(sha256sum "$RUNNER_TEMP/service-plan.json" | cut -d' ' -f1)"
		  printf 'Plan digest: sha256:%s\n' "$digest" >> "$GITHUB_STEP_SUMMARY"
		  jq -r '[.resource_changes[]?.change.actions | join("/")] | group_by(.) | map({action: .[0], count: length})' "$RUNNER_TEMP/service-plan.json" >> "$GITHUB_STEP_SUMMARY"
		  payload="$(jq -n --arg context "platform/plan/sha256:$digest" --arg url "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID" '{state:"success",context:$context,description:"Canonical OpenTofu plan is ready",target_url:$url}')"
		  curl --fail-with-body --silent --show-error --request POST --header "Authorization: Bearer $GITHUB_TOKEN" --header "Accept: application/vnd.github+json" --header "X-GitHub-Api-Version: 2026-03-10" "$GITHUB_API_URL/repos/$GITHUB_REPOSITORY/statuses/$HEAD_SHA" --data "$payload"
		  rm -f "$RUNNER_TEMP/service.tfplan" "$RUNNER_TEMP/service-plan.json"
`
