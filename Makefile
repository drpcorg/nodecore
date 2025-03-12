generate-networks:
	go run cmd/chains/init_chains.go

lint:
	golangci-lint run ./...

build: generate-networks
	go build -o $(PWD)/dshaltie.service cmd/dshaltie/main.go