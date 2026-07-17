#!/bin/sh
set -eu

dist=${1:-}
[ -d "$dist" ] || { echo "package output unavailable" >&2; exit 1; }

temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT HUP INT TERM
sensitive_pattern='(/Users/|/home/[^/[:space:]]+|https?://[^[:space:]]*(analytics|datadog|honeycomb|segment|sentry|telemetry))'

archives=$(find "$dist" -maxdepth 1 -type f \( -name 'prompt-better_*.tar.gz' -o -name 'prompt-better_*.zip' \) | wc -l | tr -d ' ')
sboms=$(find "$dist" -maxdepth 1 -type f -name '*.sbom.json' | wc -l | tr -d ' ')
[ "$archives" = 5 ] || { echo "expected five release archives" >&2; exit 1; }
[ "$sboms" = 5 ] || { echo "expected five archive SBOMs" >&2; exit 1; }
if [ ! -f "$dist/checksums.txt" ] || [ ! -x "$dist/install.sh" ] || [ ! -f "$dist/prompt-better.rb.tmpl" ]; then
  echo "release metadata incomplete" >&2
  exit 1
fi

entries=$(find "$dist" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')
[ "$entries" = 13 ] || { echo "unexpected release output" >&2; exit 1; }
find "$dist" -mindepth 1 -maxdepth 1 -print | while IFS= read -r entry; do
  if [ ! -f "$entry" ] || [ -L "$entry" ]; then
    echo "non-regular release output" >&2
    exit 1
  fi
  name=${entry##*/}
  case "$name" in
    prompt-better_*.tar.gz | prompt-better_*.zip | *.sbom.json | checksums.txt | install.sh | prompt-better.rb.tmpl) ;;
    *) echo "unexpected release output" >&2; exit 1 ;;
  esac
done

verify_names() {
  archive=$1
  case "$archive" in
    *.tar.gz)
      tar -tzf "$archive" >"$temp/list"
      tar -tvzf "$archive" | awk '$1 !~ /^-/ { exit 1 }'
      rm -rf "$temp/extract"
      mkdir "$temp/extract"
      tar -xzf "$archive" -C "$temp/extract"
      suffix=
      ;;
    *.zip)
      unzip -Z1 "$archive" >"$temp/list"
      zipinfo -l "$archive" | awk '/^[dl-][rwx-]/ && $1 !~ /^-/ { exit 1 }'
      rm -rf "$temp/extract"
      mkdir "$temp/extract"
      unzip -q "$archive" -d "$temp/extract"
      suffix=.exe
      ;;
    *) return 1 ;;
  esac
  sed 's#^\./##' "$temp/list" | LC_ALL=C sort >"$temp/actual"
  if grep -Eq '(^/|(^|/)\.\.(/|$)|\\)' "$temp/actual"; then
    echo "unsafe archive path" >&2
    return 1
  fi
  {
    printf 'LICENSE\nREADME.md\ndocs/installation.md\n'
    printf 'prompt-better%s\nprompt-better-admin%s\nprompt-better-collector%s\nprompt-better-mcp%s\n' \
      "$suffix" "$suffix" "$suffix" "$suffix"
  } | LC_ALL=C sort >"$temp/expected"
  cmp "$temp/expected" "$temp/actual" >/dev/null || { echo "unexpected archive contents" >&2; return 1; }
  find "$temp/extract" -type f -name 'prompt-better*' -print | while IFS= read -r binary; do
    if grep -aEqi "$sensitive_pattern" "$binary"; then
      echo "sensitive build metadata" >&2
      return 1
    fi
  done
}

find "$dist" -maxdepth 1 -type f \( -name 'prompt-better_*.tar.gz' -o -name 'prompt-better_*.zip' \) -print | while IFS= read -r archive; do
  verify_names "$archive"
done

find "$dist" -maxdepth 1 -type f -name '*.sbom.json' -print | while IFS= read -r sbom; do
  jq -e '.spdxVersion and .packages' "$sbom" >/dev/null
  if grep -Eqi "$sensitive_pattern" "$sbom"; then
    echo "sensitive package metadata" >&2
    exit 1
  fi
done

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -c checksums.txt >/dev/null
else
  shasum -a 256 -c checksums.txt >/dev/null
fi
