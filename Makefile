BASE_IMAGE = golang:1.26-alpine3.24
GOLANGCI_LINT_IMAGE = golangci/golangci-lint:v2.13.2
NODE_IMAGE = node:24-alpine3.24

.DEFAULT_GOAL := build

.PHONY: build frontend generate help
.PHONY: $(shell ls)

build: frontend generate
	mkdir -p binaries
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o binaries/mediamtx .

frontend: web/node_modules/.mediamtx-dependencies
	cd web && npm run build

web/node_modules/.mediamtx-dependencies: web/package.json web/package-lock.json
	cd web && npm ci
	touch $@

generate:
	go generate ./...

help:
	@echo "usage: make [action]"
	@echo ""
	@echo "available actions:"
	@echo ""
	@echo "  build            build frontend and local binary (default)"
	@echo "  frontend         build administration UI"
	@echo "  generate         generate Go build resources"
	@echo "  format           format code"
	@echo "  test             run tests"
	@echo "  test-32          run tests on a 32-bit system"
	@echo "  test-e2e         run end-to-end tests"
	@echo "  lint             run linters"
	@echo "  binaries         build binaries for all supported platforms"
	@echo "  dockerhub        build and push images to Docker Hub"
	@echo "  apidocs          generate API documentation"
	@echo ""

blank :=
define NL

$(blank)
endef

include scripts/*.mk
