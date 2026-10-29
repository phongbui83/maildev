# Developer shortcuts. Each component also builds on its own (see README).
.PHONY: help up down logs build test typecheck smoke lite-image lite-test

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/'

up:            ## full stack with docker compose (dev auth mode)
	@test -f .env || (cp .env.example .env && sed -i "s#^MAILDEV_MASTER_KEY=.*#MAILDEV_MASTER_KEY=$$(openssl rand -base64 32)#" .env && echo "created .env")
	docker compose up --build -d

down:          ## stop the stack (keeps the database volume)
	docker compose down

logs:          ## follow API logs
	docker compose logs -f api

build:         ## compile backend + typecheck/build frontend
	cd backend && go build ./... && go vet ./...
	cd frontend && npm ci --no-audit --no-fund && npm run typecheck && npm run build

test:          ## backend unit tests
	cd backend && go test ./...

smoke:         ## end-to-end suites against a backend started as described in README "Testing"
	python3 -I backend/scripts/smoke_test.py
	python3 -I backend/scripts/relay_test.py
	python3 -I backend/scripts/local_users_test.py

lite-image:    ## single-container lite edition image
	docker build -f lite/Dockerfile -t maildev/lite:local .

lite-test:     ## lite edition test suites (uses the backend scripts)
	cd lite && scripts/test.sh
