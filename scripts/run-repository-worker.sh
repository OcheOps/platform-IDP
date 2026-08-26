#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/.idp/github-app.env}"

if [[ ! -f "$ENV_FILE" || -L "$ENV_FILE" ]]; then
  printf 'GitHub App environment file must be a regular file: %s\n' "$ENV_FILE" >&2
  exit 1
fi
ENV_MODE=$(stat -c '%a' "$ENV_FILE")
ENV_OWNER=$(stat -c '%u' "$ENV_FILE")
if [[ ! "$ENV_MODE" =~ ^[0-7]00$ || "$ENV_OWNER" != "$(id -u)" ]]; then
  printf 'GitHub App environment file must be owned by the current user with owner-only permissions: %s\n' "$ENV_FILE" >&2
  exit 1
fi

GITHUB_APP_ID=""
GITHUB_APP_INSTALLATION_ID=""
GITHUB_APP_PRIVATE_KEY_FILE=""
INSTALLATION_CONFIG=""
GENERATED_DIRECTORY=""
declare -A SEEN_KEYS=()
while IFS='=' read -r key value; do
  if [[ "$key" == *$'\r'* || "$value" == *$'\r'* ]]; then
    printf 'Malformed line in GitHub App environment file: %s\n' "$ENV_FILE" >&2
    exit 1
  fi
  case "$key" in
    GITHUB_APP_ID|GITHUB_APP_INSTALLATION_ID|GITHUB_APP_PRIVATE_KEY_FILE|INSTALLATION_CONFIG|GENERATED_DIRECTORY)
      if [[ -n "${SEEN_KEYS[$key]:-}" ]]; then
        printf 'Duplicate %s in GitHub App environment file: %s\n' "$key" "$ENV_FILE" >&2
        exit 1
      fi
      SEEN_KEYS[$key]=1
      printf -v "$key" '%s' "$value"
      ;;
    *)
      printf 'Unknown or malformed line in GitHub App environment file: %s\n' "$ENV_FILE" >&2
      exit 1
      ;;
  esac
done < "$ENV_FILE"

if (( ${#SEEN_KEYS[@]} != 5 )); then
  printf 'GitHub App environment file must contain exactly five settings: %s\n' "$ENV_FILE" >&2
  exit 1
fi

if [[ ! "$GITHUB_APP_ID" =~ ^[1-9][0-9]*$ || ! "$GITHUB_APP_INSTALLATION_ID" =~ ^[1-9][0-9]*$ ]]; then
  printf 'GitHub App and installation IDs must be positive integers in %s\n' "$ENV_FILE" >&2
  exit 1
fi
if [[ -z "$GITHUB_APP_PRIVATE_KEY_FILE" || -z "$INSTALLATION_CONFIG" || -z "$GENERATED_DIRECTORY" ]]; then
  printf 'GitHub App environment file is incomplete: %s\n' "$ENV_FILE" >&2
  exit 1
fi

cd "$REPO_ROOT"
exec env \
  GITHUB_APP_ID="$GITHUB_APP_ID" \
  GITHUB_APP_INSTALLATION_ID="$GITHUB_APP_INSTALLATION_ID" \
  GITHUB_APP_PRIVATE_KEY_FILE="$GITHUB_APP_PRIVATE_KEY_FILE" \
  INSTALLATION_CONFIG="$INSTALLATION_CONFIG" \
  GENERATED_DIRECTORY="$GENERATED_DIRECTORY" \
  go run ./cmd/repository-worker "$@"
