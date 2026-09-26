# Default sandbox image for Umpteenth runs and the recommended FROM for job Dockerfiles (PLAN.md §4.9)
# The adapter injects the ump CLI at runtime and picks the uid per exec, so this image sets no USER

# uv is not packaged by Debian, so it is copied from Astral's official image
ARG UV_VERSION=latest
FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv

FROM debian:trixie-slim

# Node, Python and uv are included so npx and uvx stdio MCP servers work out of the box
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        git \
        jq \
        nodejs \
        npm \
        procps \
        python3 \
        ripgrep \
        tini \
    && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /uvx /usr/local/bin/

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

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["sleep", "infinity"]
