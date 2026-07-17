#!/bin/sh
set -eu

repo="nijanthan-dev/codex-prompt-better"
binaries="prompt-better prompt-better-mcp prompt-better-collector prompt-better-admin"
mode=install
version=
install_dir=${HOME:?HOME is required}/.local/bin

usage() {
  echo "usage: install.sh --version vX.Y.Z [--install-dir DIR] | --rollback [--install-dir DIR] | --uninstall [--install-dir DIR]" >&2
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      if [ "$mode" != install ] || [ -n "$version" ] || [ "$#" -lt 2 ]; then usage; fi
      version=$2
      shift 2
      ;;
    --install-dir) [ "$#" -ge 2 ] || usage; install_dir=$2; shift 2 ;;
    --rollback)
      if [ "$mode" != install ] || [ -n "$version" ]; then usage; fi
      mode=rollback
      shift
      ;;
    --uninstall)
      if [ "$mode" != install ] || [ -n "$version" ]; then usage; fi
      mode=uninstall
      shift
      ;;
    *) usage ;;
  esac
done

case "$install_dir" in /*) ;; *) echo "install directory must be absolute" >&2; exit 2 ;; esac
[ ! -L "$install_dir" ] || { echo "install directory must not be a symlink" >&2; exit 1; }
umask 077

sha_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "SHA-256 tool unavailable" >&2
    return 1
  fi
}

sha_text() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
  else
    echo "SHA-256 tool unavailable" >&2
    return 1
  fi
}

state_base=${XDG_STATE_HOME:-"$HOME/.local/state"}/prompt-better
state_root=$state_base/installations
state_id=$(sha_text "$install_dir")
state_dir=$state_root/$state_id
manifest=$state_dir/manifest
rollback_dir=$state_dir/rollback
if [ -L "$state_base" ] || [ -L "$state_root" ] || [ -L "$state_dir" ] || [ -L "$rollback_dir" ]; then
  echo "installer state must not use symlinks" >&2
  exit 1
fi

manifest_value() {
  key=$1
  file=$2
  awk -v key="$key" '$1 == key { if (found) exit 2; print $2; found=1 } END { if (!found) exit 1 }' "$file"
}

verify_set() {
  directory=$1
  file=$2
  [ -f "$file" ] || return 1
  for name in $binaries; do
    expected=$(manifest_value "$name" "$file") || return 1
    [ -f "$directory/$name" ] && [ ! -L "$directory/$name" ] || return 1
    actual=$(sha_file "$directory/$name") || return 1
    [ "$actual" = "$expected" ] || return 1
  done
}

verify_owned() {
  verify_set "$install_dir" "$1"
}

verify_backup() {
  verify_set "$rollback_dir" "$rollback_dir/manifest"
}

write_manifest() {
  destination=$1
  release_version=$2
  directory=$3
  temporary=$destination.$$
  {
    printf 'version %s\n' "$release_version"
    for name in $binaries; do
      digest=$(sha_file "$directory/$name") || return 1
      printf '%s %s\n' "$name" "$digest"
    done
  } >"$temporary"
  mv "$temporary" "$destination"
}

restore_from() {
  source_dir=$1
  for name in $binaries; do
    cp "$source_dir/$name" "$install_dir/.prompt-better-restore-$$-$name" || return 1
    chmod 0755 "$install_dir/.prompt-better-restore-$$-$name" || return 1
  done
  for name in $binaries; do
    mv "$install_dir/.prompt-better-restore-$$-$name" "$install_dir/$name" || return 1
  done
}

if [ "$mode" = uninstall ]; then
  verify_owned "$manifest" || { echo "installed binaries are missing, modified, or unowned" >&2; exit 1; }
  staged=$state_dir/uninstall.$$
  mkdir -p "$staged"
  moved=
  uninstall_active=true
  # shellcheck disable=SC2317 # Invoked by trap.
  cleanup_uninstall() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$uninstall_active" = true ]; then
      for restore in $moved; do mv "$staged/$restore" "$install_dir/$restore" || true; done
    fi
    exit "$status"
  }
  trap cleanup_uninstall EXIT HUP INT TERM
  for name in $binaries; do
    moved="$moved $name"
    if ! mv "$install_dir/$name" "$staged/$name"; then
      echo "binary uninstall failed" >&2
      exit 1
    fi
  done
  uninstall_active=false
  rm -rf "$state_dir"
  echo "prompt-better binaries uninstalled"
  exit 0
fi

if [ "$mode" = rollback ]; then
  verify_owned "$manifest" || { echo "installed binaries are missing, modified, or unowned" >&2; exit 1; }
  verify_backup || { echo "verified rollback unavailable" >&2; exit 1; }
  current=$state_dir/current.$$
  mkdir -p "$current"
  rollback_active=false
  # shellcheck disable=SC2317 # Invoked by trap.
  cleanup_rollback() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$rollback_active" = true ]; then
      restore_from "$current" || true
      cp "$current/manifest" "$manifest" || true
    fi
    rm -rf "$current"
    exit "$status"
  }
  trap cleanup_rollback EXIT HUP INT TERM
  cp "$manifest" "$current/manifest"
  for name in $binaries; do cp "$install_dir/$name" "$current/$name"; done
  rollback_active=true
  if ! restore_from "$rollback_dir" || ! cp "$rollback_dir/manifest" "$manifest"; then
    echo "binary rollback failed" >&2
    exit 1
  fi
  rollback_active=false
  rm -rf "$rollback_dir" "$current"
  echo "prompt-better binaries rolled back"
  exit 0
fi

[ -n "$version" ] || usage
printf '%s' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "invalid release version" >&2; exit 2; }
clean_version=${version#v}

case $(uname -s) in
  Darwin) target_os=darwin ;;
  Linux) target_os=linux ;;
  *) echo "unsupported operating system" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) target_arch=amd64 ;;
  arm64 | aarch64) target_arch=arm64 ;;
  *) echo "unsupported architecture" >&2; exit 1 ;;
esac

for tool in awk cmp curl gh grep mktemp sed tar uname; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done

asset="prompt-better_${clean_version}_${target_os}_${target_arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
temp=$(mktemp -d)
transaction_active=false
had_previous=false
candidate_rollback=
previous_rollback=
rollback_swap=false
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$transaction_active" = true ]; then
    if [ "$had_previous" = true ]; then
      backup=$candidate_rollback
      [ -d "$backup" ] || backup=$rollback_dir
      restore_from "$backup" || true
      cp "$backup/manifest" "$manifest" || true
    else
      for name in $binaries; do rm -f "$install_dir/$name"; done
      rm -f "$manifest"
    fi
    if [ -n "$previous_rollback" ] && [ -d "$previous_rollback" ]; then
      rm -rf "$rollback_dir"
      mv "$previous_rollback" "$rollback_dir" || true
    elif [ "$rollback_swap" = true ]; then
      rm -rf "$rollback_dir"
    fi
  fi
  [ -z "$candidate_rollback" ] || rm -rf "$candidate_rollback"
  rm -rf "$temp"
  for name in $binaries; do
    rm -f "$install_dir/.prompt-better-install-$$-$name" "$install_dir/.prompt-better-restore-$$-$name"
  done
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

curl --fail --location --proto '=https' --tlsv1.2 --output "$temp/checksums.txt" "$base/checksums.txt"
curl --fail --location --proto '=https' --tlsv1.2 --output "$temp/$asset" "$base/$asset"

expected=$(awk -v name="$asset" '$2 == name { if (found) exit 2; print $1; found=1 } END { if (!found) exit 1 }' "$temp/checksums.txt") || {
  echo "archive checksum unavailable" >&2
  exit 1
}
if [ "${#expected}" -ne 64 ] || ! printf '%s' "$expected" | grep -Eq '^[0-9a-f]+$'; then
  echo "archive checksum invalid" >&2
  exit 1
fi
actual=$(sha_file "$temp/$asset")
[ "$actual" = "$expected" ] || { echo "archive checksum mismatch" >&2; exit 1; }
gh attestation verify "$temp/$asset" --repo "$repo" >/dev/null || { echo "archive provenance verification failed" >&2; exit 1; }

tar -tzf "$temp/$asset" >"$temp/archive-list"
if grep -Eq '(^/|(^|/)\.\.(/|$)|\\)' "$temp/archive-list"; then
  echo "unsafe archive path" >&2
  exit 1
fi
tar -tvzf "$temp/$asset" | awk '$1 !~ /^-/ { exit 1 }' || { echo "archive contains non-regular entries" >&2; exit 1; }
{
  printf 'LICENSE\nREADME.md\ndocs/installation.md\n'
  for name in $binaries; do printf '%s\n' "$name"; done
} | LC_ALL=C sort >"$temp/expected-list"
sed 's#^\./##' "$temp/archive-list" | LC_ALL=C sort >"$temp/actual-list"
cmp "$temp/expected-list" "$temp/actual-list" >/dev/null || { echo "archive contents invalid" >&2; exit 1; }

mkdir "$temp/extract"
tar -xzf "$temp/$asset" -C "$temp/extract"
for name in $binaries; do
  if [ ! -f "$temp/extract/$name" ] || [ -L "$temp/extract/$name" ]; then
    echo "archive binary invalid" >&2
    exit 1
  fi
done
reported=$("$temp/extract/prompt-better" --version 2>/dev/null | awk '{print $2}')
[ "$reported" = "$version" ] || { echo "archive version mismatch" >&2; exit 1; }

mkdir -p "$install_dir" "$state_dir"
chmod 0700 "$state_dir"
if [ -f "$manifest" ]; then
  verify_owned "$manifest" || { echo "installed binaries are missing, modified, or unowned" >&2; exit 1; }
  had_previous=true
else
  for name in $binaries; do
    [ ! -e "$install_dir/$name" ] || { echo "destination contains unowned binary" >&2; exit 1; }
  done
fi

candidate_rollback=$state_dir/rollback.next.$$
previous_rollback=$state_dir/rollback.previous.$$
if [ "$had_previous" = true ]; then
  mkdir "$candidate_rollback"
  cp "$manifest" "$candidate_rollback/manifest"
  for name in $binaries; do cp "$install_dir/$name" "$candidate_rollback/$name"; done
fi

for name in $binaries; do
  cp "$temp/extract/$name" "$install_dir/.prompt-better-install-$$-$name"
  chmod 0755 "$install_dir/.prompt-better-install-$$-$name"
done

transaction_active=true
for name in $binaries; do
  if ! mv "$install_dir/.prompt-better-install-$$-$name" "$install_dir/$name"; then
    echo "binary installation failed" >&2
    exit 1
  fi
done
if ! write_manifest "$manifest" "$version" "$install_dir"; then
  echo "installation manifest failed" >&2
  exit 1
fi
if [ "$had_previous" = true ]; then
  if [ -d "$rollback_dir" ]; then
    mv "$rollback_dir" "$previous_rollback"
  fi
  rollback_swap=true
  if ! mv "$candidate_rollback" "$rollback_dir"; then
    echo "rollback generation update failed" >&2
    exit 1
  fi
  transaction_active=false
  rm -rf "$previous_rollback"
  rollback_swap=false
else
  transaction_active=false
fi

echo "prompt-better binaries installed"
