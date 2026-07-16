#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly image="prompt-better-act:local"

for tool in act docker; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
done

if ! docker info >/dev/null 2>&1; then
  echo "Docker Desktop is not running" >&2
  exit 1
fi

case "$(docker info --format '{{.Architecture}}')" in
  aarch64 | arm64)
    readonly platform="linux/arm64"
    ;;
  amd64 | x86_64)
    readonly platform="linux/amd64"
    ;;
  *)
    echo "unsupported Docker architecture" >&2
    exit 1
    ;;
esac

docker build \
  --file "${root}/.act/Dockerfile" \
  --platform "${platform}" \
  --tag "${image}" \
  "${root}/.act"

cd "${root}"
act workflow_dispatch \
  --action-offline-mode \
  --bind \
  --container-architecture "${platform}" \
  --network none \
  --platform "ubuntu-latest=${image}" \
  --pull=false \
  --workflows .act/workflows/ci.yml
