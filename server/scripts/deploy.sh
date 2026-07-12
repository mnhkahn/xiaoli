#!/usr/bin/env bash

# Deploy the server with the newest published cyeam-cli Git tag. Passing the
# resolved tag as a build argument lets Docker reuse the cyeam download layer
# until cyeam-cli publishes a newer release.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cyeam_repo="https://github.com/mnhkahn/cyeam-cli.git"

cyeam_version="$({
  git ls-remote --tags --refs "$cyeam_repo" \
    | awk -F/ '$3 ~ /^v?[0-9]+\.[0-9]+\.[0-9]+$/ {print $3}' \
    | sort -V \
    | tail -n 1
} )"

if [[ -z "$cyeam_version" ]]; then
  echo "could not resolve a cyeam-cli release tag" >&2
  exit 1
fi

echo "Deploying with cyeam ${cyeam_version}" >&2
cd "$repo_root"
exec fly deploy . \
  -c server/fly.toml \
  --dockerfile server/Dockerfile \
  --build-arg "CYEAM_VERSION=${cyeam_version}" \
  --build-arg "XIAOLI_SKILLS_CACHE_BUST=$(date +%Y%m%d%H%M%S)"
