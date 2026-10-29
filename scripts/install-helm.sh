#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'This installer supports Linux amd64; install Helm 4.3.1 manually on other platforms.' >&2; exit 1; }
version=4.3.1
checksum=0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
mkdir -p .cache/helm bin/linux
archive="$PWD/.cache/helm/linux.tar.gz"
if [[ ! -f "$archive" ]] || ! printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --status; then
  curl --fail --location --retry 3 -o "$archive" "https://get.helm.sh/helm-v$version-linux-amd64.tar.gz"
fi
printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check
tar -xzf "$archive" -C .cache/helm --no-same-owner linux-amd64/helm
install -m 0755 .cache/helm/linux-amd64/helm bin/linux/helm
bin/linux/helm version --short
