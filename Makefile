generate-networks:
	go run config/init_chains.go

build: generate-networks
	go build -o $(PWD)/dshaltie.service src/main.go