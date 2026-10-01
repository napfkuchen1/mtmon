VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      ?= go

.PHONY: all ui build test lint scale deploy-test release run-demo clean

all: test build

ui:                       ## build the Svelte UI into web/dist (embedded into the binary)
	cd web && npm ci --no-audit --no-fund && npm run build

build: ui                 ## static linux/amd64 binary with embedded UI
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/mtmon ./cmd/mtmon
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/flowgen ./cmd/flowgen
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/mockros ./cmd/mockros

test:                     ## unit + integration tests (race detector on)
	$(GO) vet ./...
	$(GO) test -race -count=1 ./...

lint:                     ## static analysis + shell checks
	staticcheck ./...
	cd deploy && shellcheck -x -s bash lib.sh install-mtmon.sh update-mtmon.sh uninstall-mtmon.sh && shellcheck -s sh update-geo.sh
	gofmt -l . | (! grep .)

scale:                    ## 100 clients x 30 days data set; asserts every query < 500 ms
	MTMON_SCALE=1 $(GO) test ./internal/integration -run TestScale -v -timeout 20m

deploy-test: build        ## install/update/uninstall scripts against fake Proxmox tools
	deploy/test/test-deploy.sh bin/mtmon

release: build            ## dist/mtmon-<version>-linux-amd64.tar.gz with checksums
	rm -rf dist && mkdir -p dist/mtmon
	cp bin/mtmon dist/mtmon/mtmon
	cp deploy/install-mtmon.sh deploy/update-mtmon.sh deploy/uninstall-mtmon.sh deploy/lib.sh deploy/mtmon.service deploy/relax.conf deploy/config.example.json deploy/update-geo.sh deploy/mtmon-geo.service deploy/mtmon-geo.timer dist/mtmon/
	cp README.md dist/mtmon/; cp -r docs dist/mtmon/docs
	cd dist/mtmon && sha256sum mtmon install-mtmon.sh update-mtmon.sh uninstall-mtmon.sh lib.sh mtmon.service relax.conf update-geo.sh mtmon-geo.service mtmon-geo.timer > SHA256SUMS
	cd dist && tar czf mtmon-$(VERSION)-linux-amd64.tar.gz mtmon && sha256sum mtmon-$(VERSION)-linux-amd64.tar.gz > mtmon-$(VERSION)-linux-amd64.tar.gz.sha256
	@ls -la dist

run-demo: build           ## local demo: mock routers + synthetic flows, UI on http://127.0.0.1:18443 (admin / demo-password-1)
	./scripts/demo.sh

clean:
	rm -rf bin dist web/dist/* && touch web/dist/.gitkeep
