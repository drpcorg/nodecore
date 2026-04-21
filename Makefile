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

.PHONY: build
build: generate-networks
	go build -o $(PWD)/nodecore cmd/nodecore/main.go

.PHONY: setup
setup:
	go mod tidy

.PHONY: run
run:
	NODECORE_CONFIG_PATH=test_nodecore.yml go run ./cmd/nodecore/main.go
