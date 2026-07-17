#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
mode=${1:-}
dist=${2:-"$root/dist"}

case "$mode" in
  snapshot | release) ;;
  *) echo "usage: package-release.sh <snapshot|release> [dist]" >&2; exit 2 ;;
esac
case "$dist" in
  "$root/dist" | /tmp/prompt-better-*) ;;
  *) echo "release output must be repository dist or /tmp/prompt-better-*" >&2; exit 2 ;;
esac

for tool in goreleaser syft jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

cd "$root"
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  commit_epoch=$(git show -s --format=%ct HEAD)
  if build_date=$(date -u -r "$commit_epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null); then
    :
  else
    build_date=$(date -u -d "@$commit_epoch" '+%Y-%m-%dT%H:%M:%SZ')
  fi
elif [ "$mode" = snapshot ]; then
  commit_epoch=0
  build_date=1970-01-01T00:00:00Z
else
  echo "release packaging requires a Git checkout" >&2
  exit 1
fi

set -- release --clean --skip=publish --skip=announce --parallelism=1
if [ "$mode" = snapshot ]; then
  set -- "$@" --snapshot
fi
PROMPT_BETTER_BUILD_DATE="$build_date" \
  PROMPT_BETTER_BUILD_TIMESTAMP="$commit_epoch" \
  goreleaser "$@"
if [ "$dist" != "$root/dist" ]; then
  rm -rf "$dist"
  mv "$root/dist" "$dist"
fi

for sbom in "$dist"/*.sbom.json; do
  archive=${sbom%.sbom.json}
  archive_name=${archive##*/}
  if command -v sha256sum >/dev/null 2>&1; then
    archive_digest=$(sha256sum "$archive" | awk '{print $1}')
  else
    archive_digest=$(shasum -a 256 "$archive" | awk '{print $1}')
  fi
  namespace="https://github.com/nijanthan-dev/codex-prompt-better/sbom/$archive_name-$archive_digest"
  jq --arg created "$build_date" --arg namespace "$namespace" \
    '.creationInfo.created = $created | .documentNamespace = $namespace' \
    "$sbom" >"$sbom.tmp"
  mv "$sbom.tmp" "$sbom"
done
find "$dist" -mindepth 1 -maxdepth 1 -type d -exec rm -rf {} +
rm -f "$dist/artifacts.json" "$dist/config.yaml" "$dist/metadata.json"
cp scripts/install.sh "$dist/install.sh"
chmod 0755 "$dist/install.sh"
cp packaging/homebrew/Formula/prompt-better.rb.tmpl "$dist/prompt-better.rb.tmpl"

checksum_file="$dist/checksums.txt"
checksum_temp="$dist/.checksums.$$"
trap 'rm -f "$checksum_temp"' EXIT HUP INT TERM
find "$dist" -maxdepth 1 -type f \( \
  -name 'prompt-better_*.tar.gz' -o \
  -name 'prompt-better_*.zip' -o \
  -name '*.sbom.json' -o \
  -name 'install.sh' -o \
  -name 'prompt-better.rb.tmpl' \
\) -print | LC_ALL=C sort | while IFS= read -r file; do
  name=${file##*/}
  if command -v sha256sum >/dev/null 2>&1; then
    digest=$(sha256sum "$file" | awk '{print $1}')
  else
    digest=$(shasum -a 256 "$file" | awk '{print $1}')
  fi
  printf '%s  %s\n' "$digest" "$name"
done >"$checksum_temp"
mv "$checksum_temp" "$checksum_file"
trap - EXIT HUP INT TERM

scripts/verify-package-output.sh "$dist"
