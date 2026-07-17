#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly root
readonly image="prompt-better-act:local"
readonly pg16_image="postgres:16-bookworm@sha256:92620daddcd947f8d5ab5ba66e848702fe443d87fed30c4cea8e389fd78dfc55"
readonly pg17_image="postgres:17-bookworm@sha256:4f736ae292687621d4dbe0d499ffd024a36bd2ee7d8ca6f2ccd4c800f047b394"
readonly network="prompt-better-act-$$"
readonly pg16="prompt-better-act-pg16-$$"
readonly pg17="prompt-better-act-pg17-$$"

cleanup() {
  docker rm --force "${pg16}" "${pg17}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup_stale() {
  while IFS= read -r stale_network; do
    stale_pid=${stale_network##*-}
    case "$stale_pid" in
      *[!0-9]* | "") continue ;;
    esac
    if kill -0 "$stale_pid" 2>/dev/null; then
      continue
    fi
    [ "$(docker network inspect --format '{{index .Labels "dev.prompt-better.local-ci"}}' "$stale_network" 2>/dev/null)" = true ] || continue
    [ "$(docker network inspect --format '{{index .Labels "dev.prompt-better.pid"}}' "$stale_network" 2>/dev/null)" = "$stale_pid" ] || continue
    docker ps -aq --filter "network=${stale_network}" | xargs docker rm --force >/dev/null 2>&1 || true
    docker network rm "$stale_network" >/dev/null 2>&1 || true
  done < <(docker network ls --format '{{.Name}}' | grep '^prompt-better-act-')
}

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

cleanup_stale

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
  --build-arg "VULNDB_REFRESH=$(date -u +%Y-%m-%d)" \
  --file "${root}/.act/Dockerfile" \
  --platform "${platform}" \
  --tag "${image}" \
  "${root}"

cleanup
docker network create --internal \
  --label dev.prompt-better.local-ci=true \
  --label "dev.prompt-better.pid=$$" \
  "${network}" >/dev/null
for major in 16 17; do
  name="prompt-better-act-pg${major}-$$"
  if [ "$major" = 16 ]; then postgres_image=$pg16_image; else postgres_image=$pg17_image; fi
  docker run --detach --rm \
    --env POSTGRES_DB=prompt_better_test \
    --env POSTGRES_PASSWORD=synthetic-test-only \
    --name "${name}" \
    --network "${network}" \
    --network-alias "prompt-better-act-pg${major}" \
    "${postgres_image}" >/dev/null
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
