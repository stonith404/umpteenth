#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

registry=ghcr.io/stonith404
version="development-$(git rev-parse --short HEAD)"

docker buildx build --platform linux/amd64,linux/arm64 \
  -f docker/Dockerfile --build-arg VERSION="$version" \
  -t "$registry/umpteenth:development" --push .

docker buildx build --platform linux/amd64,linux/arm64 \
  -t "$registry/umpteenth-sandbox:development" --push docker/sandbox

echo "Pushed $registry/umpteenth:development and $registry/umpteenth-sandbox:development ($version)"
