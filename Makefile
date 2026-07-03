GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: generate lint test build vet fmt

generate: ## regenerate Go from protos (requires buf, protoc-gen-go, protoc-gen-go-grpc)
	buf generate

lint:
	buf lint
	test -z "$$(gofmt -l cmd internal pkg plugins 2>/dev/null)"
	go vet ./...

test:
	go test -race ./...

build:
	go build -o taskd ./cmd/taskd
	go build -o task ./cmd/task

fmt:
	gofmt -w cmd internal pkg plugins 2>/dev/null || true
