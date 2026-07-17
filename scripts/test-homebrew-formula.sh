#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tap=nijanthan-dev/prompt-better-local-test
formula=$tap/prompt-better
temp=$(mktemp -d)
server_pid=

cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  brew_local uninstall --force "$formula" >/dev/null 2>&1 || true
  brew_local untap --force "$tap" >/dev/null 2>&1 || true
  if [ -n "$server_pid" ]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$temp"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

brew_local() {
  HOMEBREW_DEVELOPER=1 HOMEBREW_NO_AUTO_UPDATE=1 brew "$@"
}

for tool in brew curl git python3 shasum tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done
if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
  echo "Homebrew formula test requires macOS arm64" >&2
  exit 1
fi
if brew list --formula prompt-better >/dev/null 2>&1 || brew tap | grep -Fx "$tap" >/dev/null 2>&1; then
  echo "Homebrew test destination already in use" >&2
  exit 1
fi

cd "$root"
git ls-files --cached --others --exclude-standard >"$temp/files"
tar -czf "$temp/source.tar.gz" -T "$temp/files"
sha=$(shasum -a 256 "$temp/source.tar.gz" | awk '{print $1}')
commit=$(git rev-parse HEAD)
commit_epoch=$(git show -s --format=%ct HEAD)
build_date=$(date -u -r "$commit_epoch" '+%Y-%m-%dT%H:%M:%SZ')
python3 -u -m http.server 0 --bind 127.0.0.1 --directory "$temp" >"$temp/server.log" 2>&1 &
server_pid=$!
attempt=0
port=
until [ -n "$port" ]; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 20 ] || { echo "source server unavailable" >&2; exit 1; }
  port=$(sed -n 's/.* port \([0-9][0-9]*\) .*/\1/p' "$temp/server.log")
  sleep 1
done
local_url="http://127.0.0.1:$port/source.tar.gz"
curl --fail --silent --output /dev/null "$local_url"

brew_local tap-new --no-git "$tap" >/dev/null
tap_root=$(brew --repository "$tap")

install_version() {
  version=$1
  release_url="https://github.com/nijanthan-dev/codex-prompt-better/archive/refs/tags/v$version.tar.gz"
  scripts/render-homebrew-formula.sh "$version" "$release_url" "$sha" "$commit" "$build_date" "$temp/formula.rb"
  sed "s|$release_url|$local_url|" "$temp/formula.rb" >"$tap_root/Formula/prompt-better.rb"
}

install_version 0.2.0
brew_local style "$formula"
brew_local install --build-from-source "$formula"
brew_local test "$formula"
"$(brew --prefix "$formula")/bin/prompt-better" --version | grep -F 'prompt-better v0.2.0' >/dev/null

install_version 0.2.1
brew_local upgrade --build-from-source "$formula"
brew_local test "$formula"
"$(brew --prefix "$formula")/bin/prompt-better" --version | grep -F 'prompt-better v0.2.1' >/dev/null

brew_local uninstall "$formula"
if brew list --formula "$formula" >/dev/null 2>&1; then
  echo "Homebrew uninstall failed" >&2
  exit 1
fi
