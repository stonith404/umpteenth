# syntax=docker/dockerfile:1.7

# Builds the single Umpteenth image: the SPA, the in-sandbox ump CLI for both architectures, and the server embedding both

# Tags passed to "go build", e.g. e2etest for the Playwright stack
ARG BUILD_TAGS=""
ARG VERSION=dev

# Stage 1: build the frontend
# The build stages run on the build machine's platform and cross-compile, so multi-arch images need no emulation
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS frontend-builder
RUN corepack enable
WORKDIR /build
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY frontend/package.json ./frontend/
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
  pnpm --filter umpteenth-frontend install --frozen-lockfile
COPY ./frontend ./frontend/
RUN BUILD_OUTPUT_PATH=dist pnpm --filter umpteenth-frontend run build

# Stage 2: build the ump CLI and the server
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS backend-builder
ARG BUILD_TAGS
ARG VERSION
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build
COPY ./backend/go.mod ./backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY ./backend ./
COPY --from=frontend-builder /build/frontend/dist ./frontend/dist
# The MCP server embeds the docs pages, so agents can search the docs of the release they talk to
COPY ./docs/src/content/docs ./internal/mcpapi/docs/

# The ump CLI is injected into every sandbox, so it is built for both sandbox architectures and embedded
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  for arch in amd64 arm64; do \
    CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o internal/sandbox/umpbin/bin/ump-linux-$arch ./cmd/ump || exit 1; \
  done

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -tags "${BUILD_TAGS}" \
    -ldflags "-s -w -X github.com/stonith404/umpteenth/backend/internal/common.Version=${VERSION}" \
    -trimpath \
    -o /build/umpteenth \
    ./cmd/umpteenth

# Stage 3: runtime image
# It runs as root because it talks to the Docker socket; a socket proxy is the recommended way to limit that
# On Kubernetes, which needs no socket, the Helm chart runs it as a non-root user instead
FROM gcr.io/distroless/static-debian13@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0
ARG TARGETARCH
WORKDIR /app
COPY --from=backend-builder /build/umpteenth /app/umpteenth
# The Kubernetes adapter's init containers copy this ump CLI into every sandbox pod
COPY --from=backend-builder /build/internal/sandbox/umpbin/bin/ump-linux-${TARGETARCH} /usr/local/bin/ump
# Settings come from /app/config.yml and the environment; app.data_dir defaults to data, which is /app/data here
VOLUME /app/data
EXPOSE 8080 8081
# Probing every 2s while the server starts lets `depends_on: service_healthy` and `docker compose up --wait` continue as soon as it is ready
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --start-interval=2s CMD ["/app/umpteenth", "healthcheck"]
ENTRYPOINT ["/app/umpteenth"]
