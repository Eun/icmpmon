#!/usr/bin/env bash
set -euo pipefail
curl -sfL https://install.goreleaser.com/github.com/goreleaser/goreleaser.sh | bash -s -- -b /bin
apt-get update
apt-get install -y gcc-multilib
goreleaser $@