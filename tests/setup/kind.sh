#!/usr/bin/env bash
# Creates the kind cluster the Kubernetes tests run on, with a plain-HTTP registry for job images on kind's Docker network
# kindnet enforces NetworkPolicies, which the sandbox adapter refuses to run without
# It prints the registry and gateway addresses, and adds them to $GITHUB_ENV in CI
set -euo pipefail

cluster=${KIND_CLUSTER:-umpteenth}
registry=${KIND_REGISTRY:-umpteenth-registry}
# The chart's Service in values-kind.yaml listens on node port 30080, which appears on this port of the machine
http_port=${KIND_HTTP_PORT:-8080}

# Nodes read registry settings from certs.d, where the registry below gets its entry
if ! kind get clusters | grep -qx "$cluster"; then
  kind create cluster --name "$cluster" --wait 120s --config - >&2 <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  - |-
    [plugins."io.containerd.cri.v1.images".registry]
      config_path = "/etc/containerd/certs.d"
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: $http_port
        listenAddress: 127.0.0.1
YAML
fi

# The registry deletes images, which the conformance suite checks, and nodes and build pods reach it by its address on kind's network
if [ -z "$(docker ps -q --filter "name=^${registry}$")" ]; then
  docker rm -f "$registry" >/dev/null 2>&1 || true
  docker run -d --name "$registry" --network kind -e REGISTRY_STORAGE_DELETE_ENABLED=true registry:3 >/dev/null
fi
registry_ip=$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$registry")
for node in $(kind get nodes --name "$cluster"); do
  docker exec "$node" sh -c "mkdir -p /etc/containerd/certs.d/$registry_ip:5000 && printf '[host.\"http://$registry_ip:5000\"]\n' > /etc/containerd/certs.d/$registry_ip:5000/hosts.toml"
done

# Pods reach the machine running the tests through the gateway of kind's network
gateway=$(docker network inspect kind -f '{{range .IPAM.Config}}{{.Gateway}} {{end}}' | tr ' ' '\n' | grep -m1 -E '^[0-9]+\.')

echo "KIND_REGISTRY_ADDR=$registry_ip:5000"
echo "KIND_GATEWAY=$gateway"
if [ -n "${GITHUB_ENV:-}" ]; then
  echo "KIND_REGISTRY_ADDR=$registry_ip:5000" >> "$GITHUB_ENV"
  echo "KIND_GATEWAY=$gateway" >> "$GITHUB_ENV"
fi
