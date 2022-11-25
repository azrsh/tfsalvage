.ONESHELL:

bin/tfsalvage: go.mod go.sum main.go
	mkdir -p bin
	go build -o bin/tfsalvage main.go
