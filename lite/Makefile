REGISTRY ?= ghcr.io/phongbui83/maildev
VERSION  ?= $(shell date +%Y%m%d%H%M)
IMAGE    ?= $(REGISTRY)/lite

.PHONY: help sync web build image run test cert package

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/'

sync:             ## copy shared backend packages from ../backend
	scripts/sync-from-full.sh

web:              ## static export of the UI (../frontend) into internal/webui/dist
	cd ../frontend && npm ci --no-audit --no-fund && NEXT_OUTPUT=export npm run build
	rm -rf internal/webui/dist && cp -r ../frontend/out internal/webui/dist

build:            ## local binary (run `make web` first for the UI)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/maildev-lite ./cmd/maildev-lite

image:            ## container image
	cd .. && docker build -f lite/Dockerfile --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

run:              ## run the image locally (self-signed cert, data in ./data)
	mkdir -p data certs && docker compose up

test:             ## smoke/relay/local-user suites of the full edition against the lite binary (dev auth mode)
	scripts/test.sh

cert:             ## issue a 10-year certificate from the OpenBao PKI: make cert HOST=maildev.customer.local [IP=10.0.0.5]
	scripts/issue-cert.sh $(HOST) $(IP)

package:          ## air-gap bundle: make package HOST=maildev.customer.local [IP=...] -> dist/maildev-lite-<version>-<host>.tar.gz
	scripts/package.sh $(HOST) $(IP)
