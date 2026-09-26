#!/usr/bin/env bash
# Starts a throwaway Docker engine with gVisor inside a privileged docker:dind container, for running the sandbox suite under runsc without touching the host's engine
# runsc is configured the way the gVisor guide recommends, and the default sandbox image is copied over from the host's engine
# Usage: scripts/gvisor-engine.sh [up|down], then run the printed make command
set -euo pipefail

name=umpteenth-gvisor-engine
port=23750
release="${GVISOR_RELEASE:-20260921}"
image="${SANDBOX_IMAGE:-ghcr.io/stonith404/umpteenth-sandbox:latest}"

if [ "${1:-up}" = "down" ]; then
  docker rm -f "$name" >/dev/null
  exit 0
fi

# Install runsc from the pinned release, checked against its published digest, before the inner engine starts
docker run -d --privileged --name "$name" -e DOCKER_TLS_CERTDIR= -p "127.0.0.1:$port:2375" --entrypoint sh docker:dind -c "
set -e
mkdir -p /opt/gvisor && cd /opt/gvisor
url=https://storage.googleapis.com/gvisor/releases/release/$release/\$(uname -m)
wget -q \$url/gvisor.tar.bz2 \$url/gvisor.tar.bz2.sha512
sha512sum -c gvisor.tar.bz2.sha512
tar -xjf gvisor.tar.bz2 && rm gvisor.tar.bz2
cp -r runsc containerd-shim-runsc-v1 gvisor-bin /usr/local/bin/
mkdir -p /etc/docker
echo '{\"runtimes\":{\"runsc\":{\"path\":\"/usr/local/bin/runsc\",\"runtimeArgs\":[\"--overlay2=none\",\"--file-access=shared\"]}}}' > /etc/docker/daemon.json
exec dockerd-entrypoint.sh dockerd --host=tcp://0.0.0.0:2375 --host=unix:///var/run/docker.sock
" >/dev/null

# Wait for the inner engine, which starts after the download
for _ in $(seq 1 90); do
  if DOCKER_HOST="tcp://127.0.0.1:$port" docker info >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
DOCKER_HOST="tcp://127.0.0.1:$port" docker info --format '{{range $name, $_ := .Runtimes}}{{$name}} {{end}}' | grep -qw runsc

# The relay inside the inner engine reaches the test broker through the outer engine's host address
docker save "$image" | DOCKER_HOST="tcp://127.0.0.1:$port" docker load >/dev/null
host="$(docker exec "$name" getent hosts host.docker.internal | awk '{print $1}')"

echo "gVisor engine ready, run the sandbox suite with:"
echo "  DOCKER_HOST=tcp://127.0.0.1:$port SANDBOXTEST_RUNTIME=runsc SANDBOXTEST_BROKER_HOST=$host make test-integration"
