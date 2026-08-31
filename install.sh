#!/bin/sh
# Install the latest pitwall release into a bin directory.
#   curl -fsSL https://raw.githubusercontent.com/sur1cat/pitwall/main/install.sh | sh
set -eu

REPO="sur1cat/pitwall"
BIN="pitwall"
PREFIX="${PREFIX:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os (on Windows download the .zip from the releases page)" >&2; exit 1 ;;
esac

tag=${VERSION:-$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)}
[ -n "$tag" ] || { echo "could not determine latest release" >&2; exit 1; }
ver=${tag#v}

url="https://github.com/$REPO/releases/download/$tag/${BIN}_${ver}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $BIN $tag ($os/$arch)"
curl -fsSL "$url" -o "$tmp/$BIN.tar.gz"
tar -xzf "$tmp/$BIN.tar.gz" -C "$tmp"
mkdir -p "$PREFIX"
install -m 0755 "$tmp/$BIN" "$PREFIX/$BIN"

echo "installed $PREFIX/$BIN"
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) echo "note: $PREFIX is not on your PATH" ;;
esac
