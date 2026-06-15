-include .env
export

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || true)
LDFLAGS := -X github.com/drpcorg/nodecore/internal/buildinfo.Version=$(VERSION) -X github.com/drpcorg/nodecore/internal/buildinfo.GitSHA=$(GIT_SHA)

.PHONY: dshackle-proto-gen
dshackle-proto-gen:
	mkdir -p pkg/dshackle
	protoc -I ./emerald-grpc/proto \
		--proto_path=emerald-grpc/proto \
		--go_out=pkg/dshackle \
		--go-grpc_out=pkg/dshackle \
		--go_opt=paths=source_relative \
		--go_opt=Mblockchain.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		--go_opt=Mcommon.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		--go_opt=Mauth.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		--go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mblockchain.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		--go-grpc_opt=Mcommon.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		--go-grpc_opt=Mauth.proto=github.com/drpcorg/nodecore/pkg/dshackle \
		blockchain.proto common.proto auth.proto

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
	go test -tags=e2e ./test/e2e/... -count=1 -timeout=20m -v

.PHONY: build
build: generate-networks
	go build -ldflags "$(LDFLAGS)" -o $(PWD)/nodecore cmd/nodecore/main.go

.PHONY: setup
setup:
	go mod tidy

.PHONY: run
run:
	go run ./cmd/nodecore/main.go
