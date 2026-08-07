#!/bin/sh
# Installs the latest (or a pinned) scaffold-cli release for Linux/macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/yusronMu77/scaffold-cli/main/install.sh | sh
#
# Override the version with SCAFFOLD_CLI_VERSION=v0.3.0, and the install directory with
# SCAFFOLD_CLI_INSTALL_DIR=/usr/local/bin (defaults to $HOME/.local/bin).
set -eu

repo="yusronMu77/scaffold-cli"
install_dir="${SCAFFOLD_CLI_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *)
    echo "error: unsupported OS '$(uname -s)' - use install.ps1 on Windows" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *)
    echo "error: unsupported architecture '$(uname -m)'" >&2
    exit 1
    ;;
esac

version="${SCAFFOLD_CLI_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" \
    | grep '"tag_name"' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  if [ -z "$version" ]; then
    echo "error: could not determine the latest release; set SCAFFOLD_CLI_VERSION explicitly" >&2
    exit 1
  fi
fi

archive="scaffold-cli_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

echo "Downloading scaffold-cli ${version} for ${os}/${arch}..."
curl -fsSL "${base_url}/${archive}" -o "${work_dir}/${archive}"
curl -fsSL "${base_url}/checksums.txt" -o "${work_dir}/checksums.txt"

echo "Verifying checksum..."
(
  cd "$work_dir"
  expected=$(grep "  ${archive}\$" checksums.txt | cut -d' ' -f1)
  if [ -z "$expected" ]; then
    echo "error: ${archive} not listed in checksums.txt" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$archive" | cut -d' ' -f1)
  else
    actual=$(shasum -a 256 "$archive" | cut -d' ' -f1)
  fi
  if [ "$expected" != "$actual" ]; then
    echo "error: checksum mismatch for ${archive}" >&2
    exit 1
  fi
)

tar -xzf "${work_dir}/${archive}" -C "$work_dir" scaffold

mkdir -p "$install_dir"
mv "${work_dir}/scaffold" "${install_dir}/scaffold"
chmod +x "${install_dir}/scaffold"

echo "Installed scaffold-cli ${version} to ${install_dir}/scaffold"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    echo ""
    echo "${install_dir} is not on your PATH. Add it, e.g.:"
    echo "  export PATH=\"${install_dir}:\$PATH\""
    ;;
esac

"${install_dir}/scaffold" --version
