GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: generate generate-web lint test test-web build build-web web extensions fmt

generate: ## regenerate Go from protos (requires buf, protoc-gen-go, protoc-gen-connect-go)
	buf generate

generate-web: ## regenerate TypeScript from protos (requires web/node_modules)
	buf generate --template buf.gen.web.yaml

lint:
	buf lint
	test -z "$$(gofmt -l cmd internal pkg extensions 2>/dev/null)"
	go vet ./...

test:
	go test -race ./...

test-web:
	cd web && npm run typecheck && npm test

build:
	go build -o taskd ./cmd/taskd
	go build -o task ./cmd/task
	go build -o task-mcp ./cmd/task-mcp

extensions: ## build the in-tree extensions (syncer binaries + web bundles)
	go build -o extensions/ics/task-sync-ics ./extensions/ics
	go build -o extensions/gcal/task-sync-gcal ./extensions/gcal
	go build -o extensions/github/task-sync-github ./extensions/github
	node extensions/build-web.mjs extensions/gcal
	node extensions/build-web.mjs extensions/github

web: ## build the web UI bundle into internal/webui/dist
	cd web && npm install && npm run build

build-web: web ## build taskd with the web UI embedded
	go build -tags webui -o taskd ./cmd/taskd
	go build -o task ./cmd/task
	go build -o task-mcp ./cmd/task-mcp

fmt:
	gofmt -w cmd internal pkg extensions 2>/dev/null || true
