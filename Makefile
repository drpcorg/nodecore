-include .env
export

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || true)
LDFLAGS := -X github.com/drpcorg/nodecore/internal/buildinfo.Version=$(VERSION) -X github.com/drpcorg/nodecore/internal/buildinfo.GitSHA=$(GIT_SHA)

.PHONY: stats-proto-gen
stats-proto-gen:
	protoc -I internal/stats/protobuf \
    		--proto_path=internal/stats/protobuf \
    		--go_out=internal/stats/api \
    		--go-grpc_out=internal/stats/api \
    		stats_request.proto


.PHONY: generate-networks
generate-networks:
	go run cmd/chains/init_chains.go

# Regenerates internal/quorum/provider_keys.yaml from the provider-config of
# the infra repo. Needs sops access to configs/provider-config/secrets.yaml.
.PHONY: provider-keys
provider-keys:
	@test -n "$(INFRA)" || { echo "set INFRA=<path to the infra repo>" >&2; exit 1; }
	sops -d --extract '["rsaKeys"]' $(INFRA)/configs/provider-config/secrets.yaml | \
		go run ./cmd/genproviderkeys -values $(INFRA)/configs/provider-config/values.yaml -keys -

# Converts a dshackle config into a nodecore one; the report of what was not carried over
# goes to stderr. FORCE=1 overwrites an existing OUT file.
#   make convert-dshackle IN=/path/dshackle.yaml OUT=/path/nodecore.yml
.PHONY: convert-dshackle
convert-dshackle: generate-networks
	@go run ./cmd/dshackle-convert -in "$(IN)" -out "$(OUT)" $(if $(FORCE),-force)

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: test
test:
	go test -race -p 8 ./...

E2E_VERSION ?= e2e-version
E2E_GIT_SHA ?= e2e-git-sha


.PHONY: build-e2e-images
build-e2e-images:
	docker build -t nodecore-e2e:latest --build-arg VERSION=$(E2E_VERSION) --build-arg GIT_SHA=$(E2E_GIT_SHA) .
	docker build -t nodecore-e2e-hardhat:latest test/e2e/internal/hardhat


.PHONY: test-e2e-grpc
test-e2e-grpc: build-e2e-images
	go test -tags=e2e ./test/e2e/grpc -count=1 -timeout=20m -v

.PHONY: test-e2e-http
test-e2e-http: build-e2e-images
	go test -tags=e2e ./test/e2e/http -count=1 -timeout=20m -v

.PHONY: test-e2e
test-e2e: build-e2e-images
	go test -tags=e2e ./test/e2e/grpc -count=1 -timeout=20m -v
	go test -tags=e2e ./test/e2e/http -count=1 -timeout=20m -v

.PHONY: build
build: generate-networks
	go build -ldflags "$(LDFLAGS)" -o $(PWD)/nodecore cmd/nodecore/main.go

.PHONY: setup
setup:
	go mod tidy

.PHONY: run
run:
	go run ./cmd/nodecore/main.go
