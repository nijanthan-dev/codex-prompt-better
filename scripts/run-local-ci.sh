#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly root
readonly image="prompt-better-act:local"
readonly network="prompt-better-act-$$"
readonly pg16="prompt-better-act-pg16-$$"
readonly pg17="prompt-better-act-pg17-$$"

cleanup() {
  docker rm --force "${pg16}" "${pg17}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

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
  "${root}"

cleanup
docker network create "${network}" >/dev/null
for major in 16 17; do
  name="prompt-better-act-pg${major}-$$"
  docker run --detach --rm \
    --env POSTGRES_DB=prompt_better_test \
    --env POSTGRES_PASSWORD=synthetic-test-only \
    --name "${name}" \
    --network "${network}" \
    --network-alias "prompt-better-act-pg${major}" \
    "postgres:${major}-bookworm" >/dev/null
  until docker exec "${name}" pg_isready --quiet -U postgres -d prompt_better_test; do
    sleep 1
  done
done

cd "${root}"
act workflow_dispatch \
  --action-offline-mode \
  --bind \
  --container-architecture "${platform}" \
  --network "${network}" \
  --platform "ubuntu-latest=${image}" \
  --pull=false \
  --workflows .act/workflows/ci.yml

if [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ]; then
  "${root}/scripts/test-homebrew-formula.sh"
fi
