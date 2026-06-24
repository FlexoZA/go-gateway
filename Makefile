# Convenience targets. Go runs inside the golang Docker image so no local Go
# toolchain is required (set GO_LOCAL=1 to use a locally installed go instead).
GO_IMAGE ?= golang:1.23-bookworm
UNIT ?= howen

ifdef GO_LOCAL
GO = go
else
GO = docker run --rm -v $(CURDIR):/app -w /app \
	-e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
	$(GO_IMAGE) go
endif

.PHONY: build test vet image run-howen new provision golden

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Build a unit-type image: make image UNIT=howen
image:
	docker build -f deploy/Dockerfile --build-arg UNIT=$(UNIT) -t device-gateway-$(UNIT) .

run-howen:
	docker compose -f deploy/docker-compose.yml up --build

# Scaffold a new unit type's code: make new UNIT=teltonika
new:
	scripts/new-gateway.sh $(UNIT)

# Provision a server for an existing unit (lean image + per-unit stack):
#   make provision UNIT=howen
provision:
	scripts/provision-server.sh $(UNIT)

# Regenerate the webhook golden file from the original JS adapter.
golden:
	node tools/gen-webhook-golden.mjs

# Add or reset a front-end user (prompts for the password, no echo):
#   export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
#   make adduser EMAIL=alice@dfm.co
adduser:
	docker run --rm -it --network host -e DATABASE_URL \
		-v $(CURDIR):/app -w /app -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
		$(GO_IMAGE) go run ./cmd/adduser --email $(EMAIL)

# Manage HTTP API keys (create/list/revoke). DATABASE_URL must be exported.
#   make apikey ARGS='create --name frontend'
#   make apikey ARGS='list'
#   make apikey ARGS='revoke --prefix dgw_AbCd'
apikey:
	docker run --rm --network host -e DATABASE_URL \
		-v $(CURDIR):/app -w /app -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
		$(GO_IMAGE) go run ./cmd/apikey $(ARGS)
