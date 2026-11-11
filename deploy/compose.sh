#!/bin/sh
set -eu

usage() {
  printf 'Usage: %s <dev|test|prod> <compose command> [args...]\n' "$0" >&2
  exit 2
}

[ "$#" -ge 2 ] || usage
deploy_environment=$1
case "$deploy_environment" in
  dev|test|prod) ;;
  *) usage ;;
esac
shift

deploy_dir=$(CDPATH= cd "$(dirname "$0")" && pwd -P)
deploy_environment_dir="$deploy_dir/environments/$deploy_environment"
deploy_env_file=${DEPLOY_ENV_FILE:-"$deploy_environment_dir/.env"}

if [ ! -f "$deploy_env_file" ]; then
  printf 'Deployment env file not found: %s\n' "$deploy_env_file" >&2
  printf 'Restore the environment .env or set DEPLOY_ENV_FILE.\n' >&2
  exit 2
fi

exec docker compose \
  --env-file "$deploy_env_file" \
  -f "$deploy_dir/compose.yaml" \
  -f "$deploy_environment_dir/compose.yaml" \
  "$@"
