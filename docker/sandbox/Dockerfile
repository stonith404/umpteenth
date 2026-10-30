# Default sandbox image for Umpteenth runs and the recommended FROM for job Dockerfiles
# The adapter injects the ump CLI at runtime, runs it as the entrypoint and picks the uid per exec, so this image sets no ENTRYPOINT or USER

# uv is not packaged by Debian, so it is copied from Astral's official image
# The tag is written out instead of passed as a build arg so Dependabot can bump it
FROM ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 AS uv

# Node comes from the official image, since Debian's Node 20 ignores the proxy variables that route internet sandboxes through the egress proxy, which Node 24 honors with NODE_USE_ENV_PROXY
FROM node:24-trixie-slim@sha256:8ec5d7557396cfe32d21c3f9c13072355ceab22b584578ca4bb28af31120cffe AS node

FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

# Node, Python, pip and uv are included so npx and uvx stdio MCP servers work out of the box
# The SSH client lets git reach SSH remotes, which Umpteenth routes through the egress proxy with ump connect
# Python's requests is included since scripts reach for it first, and it honors the egress proxy variables
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        git \
        jq \
        openssh-client \
        procps \
        python3 \
        python3-pip \
        python3-requests \
        ripgrep \
    && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=node /usr/local/bin/node /usr/local/bin/
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && ln -s ../lib/node_modules/corepack/dist/corepack.js /usr/local/bin/corepack

# Debian marks its Python as externally managed, which makes `uv pip install --system` and `pip install` fail
# A sandbox is disposable, so job Dockerfiles may install into the system Python like in any other throwaway image
ENV UV_BREAK_SYSTEM_PACKAGES=1 \
    PIP_BREAK_SYSTEM_PACKAGES=1

# The adapter maps the agent role to uid 1000 and the mcp role to uid 1001
RUN groupadd --gid 1000 agent \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash agent \
    && groupadd --gid 1001 mcp \
    && useradd --uid 1001 --gid 1001 --create-home --shell /bin/bash mcp \
    && install -d -o 1000 -g 1000 -m 0755 /workspace /ump

WORKDIR /workspace
