PLUGIN_NAME := nomad-autoscaler-ovh
BUILD_DIR   := build

.PHONY: build test vet clean fmt

build:
	go build -o $(BUILD_DIR)/$(PLUGIN_NAME) .

test:
	go test ./... -v -count=1

vet:
	go vet ./...

fmt:
	gofmt -s -w .

e2e: build
	go test -tags=e2e -v -count=1 -timeout=120m ./e2e

dev:
	nomad agent -dev -config=$(abspath e2e/agent.hcl)

clean:
	rm -rf $(BUILD_DIR)
