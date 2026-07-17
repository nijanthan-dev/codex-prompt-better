#!/bin/sh
set -eu

[ "$#" = 6 ] || { echo "usage: render-homebrew-formula.sh VERSION URL SHA256 COMMIT BUILD_DATE OUTPUT" >&2; exit 2; }
version=$1
url=$2
sha=$3
commit=$4
build_date=$5
output=$6
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)

printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "invalid version" >&2; exit 2; }
expected_url="https://github.com/nijanthan-dev/codex-prompt-better/archive/refs/tags/v$version.tar.gz"
[ "$url" = "$expected_url" ] || { echo "invalid source URL" >&2; exit 2; }
if [ "${#sha}" -ne 64 ] || ! printf '%s' "$sha" | grep -Eq '^[0-9a-f]+$'; then echo "invalid SHA-256" >&2; exit 2; fi
if [ "${#commit}" -ne 40 ] || ! printf '%s' "$commit" | grep -Eq '^[0-9a-f]+$'; then echo "invalid commit" >&2; exit 2; fi
printf '%s' "$build_date" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' || { echo "invalid build date" >&2; exit 2; }

sed \
  -e "s|@VERSION@|$version|g" \
  -e "s|@SOURCE_URL@|$url|g" \
  -e "s|@SHA256@|$sha|g" \
  -e "s|@COMMIT@|$commit|g" \
  -e "s|@BUILD_DATE@|$build_date|g" \
  "$root/packaging/homebrew/Formula/prompt-better.rb.tmpl" >"$output"
