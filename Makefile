GO ?= go
NPM ?= npm
VERSION ?= dev
RELEASE_VERSION ?= v0.1.1
OUTPUT ?= artifacts/release/$(RELEASE_VERSION)
VERSION_FLAG = -X github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo.Version=$(VERSION)

.PHONY: help web build test check test-browser dev release sizes
help:
	@echo 'make build         构建内嵌 Web 的本机程序到 bin/'
	@echo 'make test          Go 竞态测试与前端单元测试'
	@echo 'make check         类型、Go vet、Shell 和工作流检查'
	@echo 'make test-browser  对已启动的 18788 预览执行浏览器回归'
	@echo 'make dev           启动后端（默认 8787）'
	@echo 'make release RELEASE_VERSION=vX.Y.Z OUTPUT=空目录'
	@echo 'make sizes OUTPUT=发布目录  生成体积报告'
web:
	$(NPM) --prefix web run build
build: web
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w $(VERSION_FLAG)' -o bin/miproxy-panel ./cmd/miproxy-panel
	ln -sf miproxy-panel bin/miproxy
test: web
	$(GO) test -race ./...
	$(NPM) --prefix web run test:unit
check: web
	$(GO) vet ./...
	sh -n install.sh
	sh -n deploy/install.sh
	sh -n scripts/release/build.sh
test-browser:
	$(NPM) --prefix web run test:browser
dev: web
	$(GO) run ./cmd/miproxy-panel serve
release: web
	sh scripts/release/build.sh $(RELEASE_VERSION) $(OUTPUT)
sizes:
	python3 scripts/release/sizes.py --release-dir $(OUTPUT)
