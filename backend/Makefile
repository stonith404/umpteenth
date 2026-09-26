UMP_BIN_DIR := internal/sandbox/umpbin/bin
UMP_ARCHES := amd64 arm64

.PHONY: ump test test-integration lint gen

# Cross-compile the in-sandbox CLI for every sandbox architecture, to be embedded by internal/sandbox/umpbin
ump:
	@for arch in $(UMP_ARCHES); do \
		echo "building $(UMP_BIN_DIR)/ump-linux-$$arch"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "-s -w" -o $(UMP_BIN_DIR)/ump-linux-$$arch ./cmd/ump || exit 1; \
	done

test:
	go test -tags exclude_frontend,exclude_ump,unit ./...

# Runs the sandbox conformance suite against the local Docker daemon
test-integration:
	go test -tags exclude_frontend,exclude_ump,integration ./internal/sandbox/...

lint:
	golangci-lint run

gen:
	./scripts/sqlc.sh
