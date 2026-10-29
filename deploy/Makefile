# Run from the deploy repo with backend/ and frontend/ cloned next to it:
#   git clone git@git.example.com:phongbui83/maildev/backend.git
#   git clone git@git.example.com:phongbui83/maildev/frontend.git
#   git clone git@git.example.com:phongbui83/maildev/deploy.git
REGISTRY ?= ghcr.io/phongbui83/maildev
TAG      ?= $(shell date +%Y%m%d%H%M)
NS       ?= maildev
BACKEND  ?= ../backend
FRONTEND ?= ../frontend

.PHONY: build test lint run-api run-web images push deploy secrets logs

build:            ## compile backend + typecheck/build frontend
	cd $(BACKEND) && go build ./... && go vet ./...
	cd $(FRONTEND) && npm run typecheck && npm run build

test:             ## backend unit tests
	cd $(BACKEND) && go test ./...

run-api:          ## run backend locally (expects env, see docker-compose.yml / .env.example)
	cd $(BACKEND) && go run ./cmd/maildev

run-web:          ## run Next.js dev server proxying /api to localhost:8080
	cd $(FRONTEND) && npm run dev

images:           ## build container images
	docker build -t $(REGISTRY)/api:$(TAG) -t $(REGISTRY)/api:latest $(BACKEND)
	docker build -t $(REGISTRY)/web:$(TAG) -t $(REGISTRY)/web:latest $(FRONTEND)

push: images      ## push images to the internal registry
	docker push $(REGISTRY)/api:$(TAG) && docker push $(REGISTRY)/api:latest
	docker push $(REGISTRY)/web:$(TAG) && docker push $(REGISTRY)/web:latest

openbao:          ## one-time OpenBao Transit + AppRole setup (needs BAO_ADDR/BAO_TOKEN)
	NS=$(NS) bash openbao/setup.sh

secrets:          ## create maildev-secrets and ad-ca in the cluster (idempotent)
	NS=$(NS) bash k8s/make-secrets.sh

deploy:           ## apply manifests with the given TAG
	cd k8s && kustomize edit set image $(REGISTRY)/api:$(TAG) $(REGISTRY)/web:$(TAG) && kubectl apply -k .
	kubectl -n $(NS) rollout status deploy/maildev-api
	kubectl -n $(NS) rollout status deploy/maildev-web

logs:
	kubectl -n $(NS) logs -f deploy/maildev-api
