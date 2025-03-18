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
	go build -o $(PWD)/dshaltie.service cmd/dshaltie/main.go