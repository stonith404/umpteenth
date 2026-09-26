# syntax=docker/dockerfile:1.7

# Builds the single Umpteenth image: the SPA, the in-sandbox ump CLI for both architectures, and the server embedding both

# Tags passed to "go build", e.g. e2etest for the Playwright stack
ARG BUILD_TAGS=""
ARG VERSION=dev

# Stage 1: build the frontend
FROM node:24-alpine AS frontend-builder
RUN corepack enable
WORKDIR /build
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY frontend/package.json ./frontend/
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
  pnpm --filter umpteenth-frontend install --frozen-lockfile
COPY ./frontend ./frontend/
RUN BUILD_OUTPUT_PATH=dist pnpm --filter umpteenth-frontend run build

# Stage 2: build the ump CLI and the server
FROM golang:1.27.1-alpine AS backend-builder
ARG BUILD_TAGS
ARG VERSION
WORKDIR /build
COPY ./backend/go.mod ./backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY ./backend ./
COPY --from=frontend-builder /build/frontend/dist ./frontend/dist

# The ump CLI is injected into every sandbox, so it is built for both sandbox architectures and embedded
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  for arch in amd64 arm64; do \
    CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o internal/sandbox/umpbin/bin/ump-linux-$arch ./cmd/ump || exit 1; \
  done

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS=linux go build \
    -tags "${BUILD_TAGS}" \
    -ldflags "-s -w -X github.com/stonith404/umpteenth/backend/internal/common.Version=${VERSION}" \
    -trimpath \
    -o /build/umpteenth \
    ./cmd/umpteenth

# Stage 3: runtime image
# It runs as root because it talks to the Docker or Podman socket; rootless Podman or a socket proxy is the recommended way to limit that (PLAN.md §4.7.2)
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=backend-builder /build/umpteenth /app/umpteenth
# Settings come from /app/config.yml and the environment; app.data_dir defaults to data, which is /app/data here
VOLUME /app/data
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/app/umpteenth", "healthcheck"]
ENTRYPOINT ["/app/umpteenth"]
