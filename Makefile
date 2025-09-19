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
	go build -o $(PWD)/dsheltie cmd/dsheltie/main.go

.PHONY: setup
setup:
	go mod tidy

.PHONY: run
run:
	go run ./cmd/dsheltie/main.go