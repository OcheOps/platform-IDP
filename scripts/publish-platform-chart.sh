#!/usr/bin/env bash
set -euo pipefail

# Publishes the shared chart to the private repository consumed by Argo CD.
# Authentication is provided by the caller's gh login; no token is written.
organization="${1:-${GITHUB_ORGANIZATION:-}}"
if [[ -z "$organization" ]]; then
  echo "usage: $0 <github-organization>" >&2
  exit 2
fi

command -v gh >/dev/null || { echo "gh CLI is required" >&2; exit 1; }
gh auth status >/dev/null

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../charts/service" && pwd)"
temporary="$(mktemp -d)"
cleanup() { rm -rf "$temporary"; }
trap cleanup EXIT
cp -R "$chart_dir"/. "$temporary"/
cd "$temporary"
git init -q
git checkout -q -b main
git add .
git -c user.name='Platform IDP' -c user.email='platform-idp@localhost' commit -qm 'Publish shared service chart'

repository="$organization/platform-charts"
if gh repo view "$repository" >/dev/null 2>&1; then
  remote="$(gh repo view "$repository" --json sshUrl --jq .sshUrl)"
else
  gh repo create "$repository" --private --description 'Shared Platform IDP Helm charts' >/dev/null
  remote="$(gh repo view "$repository" --json sshUrl --jq .sshUrl)"
fi
git remote add origin "$remote"
git push --force-with-lease origin main
echo "published charts/service to $repository"
