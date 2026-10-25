#!/bin/sh
# Installs local-agent from its GitHub release.
#
#   curl -fsSL https://local-agent.dev/install.sh | sh
#
# The archive is checked against the release's SHA256SUMS.txt before it is
# unpacked into ~/.local/share/local-agent/<version>; ~/.local/bin/local-agent
# links to it. The sums come from the same release: they catch a corrupt or
# truncated download, they are not a signature. Nothing runs as root and nothing outside those two folders
# changes. Settings, all optional:
#
#   LOCAL_AGENT_VERSION        a release tag, such as v0.52.0 (default: latest)
#   LOCAL_AGENT_HOME           where versions are unpacked
#                              (default: ~/.local/share/local-agent)
#   LOCAL_AGENT_BIN_DIR        where the command link goes (default: ~/.local/bin)
#   LOCAL_AGENT_DOWNLOAD_BASE  a mirror of the release downloads
#                              (needs LOCAL_AGENT_VERSION)

set -eu

repo="abdul-hamid-achik/local-agent"
home_dir="${LOCAL_AGENT_HOME:-$HOME/.local/share/local-agent}"
bin_dir="${LOCAL_AGENT_BIN_DIR:-$HOME/.local/bin}"
base="${LOCAL_AGENT_DOWNLOAD_BASE:-https://github.com/$repo/releases/download}"

fail() {
  printf 'local-agent install: %s\n' "$1" >&2
  exit 1
}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "no release for $(uname -s); build from source: https://local-agent.dev/getting-started" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) fail "no release for $(uname -m); build from source: https://local-agent.dev/getting-started" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

version="${LOCAL_AGENT_VERSION:-}"
if [ -z "$version" ]; then
  [ -z "${LOCAL_AGENT_DOWNLOAD_BASE:-}" ] || fail "LOCAL_AGENT_DOWNLOAD_BASE needs LOCAL_AGENT_VERSION"
  # The latest release's page redirects to its tag.
  latest="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")" ||
    fail "could not reach GitHub to find the latest release"
  version="${latest##*/}"
fi
# The tag names a folder below LOCAL_AGENT_HOME: nothing that could leave it.
case "$version" in
  *[!A-Za-z0-9._-]* | *..*) fail "not a release tag: $version" ;;
esac
case "$version" in
  v[0-9]*) ;;
  [0-9]*) version="v$version" ;;
  *) fail "not a release tag: $version" ;;
esac

# The command link replaces only a link this installer made: any other
# local-agent there (a wrapper script, another install) stays untouched.
link="$bin_dir/local-agent"
if [ -e "$link" ] || [ -L "$link" ]; then
  case "$(readlink "$link" 2>/dev/null || true)" in
    "$home_dir"/*) ;;
    *) fail "$link exists and is not a link to $home_dir; move it away or set LOCAL_AGENT_BIN_DIR" ;;
  esac
fi

archive="local-agent_${version#v}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading local-agent %s for %s/%s\n' "$version" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base/$version/$archive" ||
  fail "$version has no $archive (releases before v0.52.0 are source only)"
curl -fsSL -o "$tmp/SHA256SUMS.txt" "$base/$version/SHA256SUMS.txt" ||
  fail "$version has no SHA256SUMS.txt"

want="$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1 }' "$tmp/SHA256SUMS.txt")"
[ -n "$want" ] || fail "SHA256SUMS.txt does not list $archive"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
else
  got="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
fi
[ "$got" = "$want" ] || fail "$archive does not match its checksum (expected $want, got $got); nothing was installed"

target="$home_dir/$version"
trap 'rm -rf "$tmp" "$target.partial"' EXIT INT TERM
rm -rf "$target.partial"
mkdir -p "$target.partial"
tar -xzf "$tmp/$archive" -C "$target.partial"
[ -x "$target.partial/local-agent" ] || fail "$archive holds no local-agent executable"
if [ "$os" = darwin ]; then
  xattr -dr com.apple.quarantine "$target.partial" 2>/dev/null || true
fi
rm -rf "$target"
mv "$target.partial" "$target"

mkdir -p "$bin_dir"
ln -sfn "$target/local-agent" "$link"

printf 'Installed %s\n' "$("$link" --version)"
printf '  %s -> %s\n' "$link" "$target/local-agent"
# The hint prints $PATH itself, for the user to paste.
# shellcheck disable=SC2016
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) printf '\n%s is not on your PATH. Add it, for example:\n  export PATH="%s:$PATH"\n' "$bin_dir" "$bin_dir" ;;
esac
if ! command -v ollama >/dev/null 2>&1; then
  printf '\nlocal-agent needs Ollama: https://ollama.com/download\n'
fi
