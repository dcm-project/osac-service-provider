BINARY_NAME := osac-service-provider
MOCK_BINARY_NAME := osac-mock-provider
AAP_MOCK_BINARY_NAME := osac-aap-mock

# CONTAINER_ENGINE: container runtime command. Set to override; otherwise auto-detect podman or docker.
CONTAINER_ENGINE ?= $(shell \
	if command -v podman >/dev/null 2>&1; then \
		echo podman; \
	elif command -v docker >/dev/null 2>&1; then \
		echo docker; \
	fi)

# CONTAINER_IMAGE_NAME: FQDN (without tag) of the container image. Set to override
CONTAINER_IMAGE_NAME ?= quay.io/dcm-project/${BINARY_NAME}

# MOCK_CONTAINER_IMAGE_NAME: FQDN (without tag) of the mock provider's own
# container image (Phase 1 of osac-service-provider#17 / FLPATH-4759). Set
# to override.
MOCK_CONTAINER_IMAGE_NAME ?= quay.io/dcm-project/${MOCK_BINARY_NAME}

# AAP_MOCK_CONTAINER_IMAGE_NAME: FQDN (without tag) of the AAP mock's own
# container image (Tier B Phase 2, osac-service-provider#44). Set to
# override.
AAP_MOCK_CONTAINER_IMAGE_NAME ?= quay.io/dcm-project/${AAP_MOCK_BINARY_NAME}

# CONTAINER_IMAGE_TAG: Container image tag. Set to override; otherwise git short hash is used
CONTAINER_IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

build:
	go build -o bin/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

build-mock-provider:
	go build -o bin/$(MOCK_BINARY_NAME) ./cmd/$(MOCK_BINARY_NAME)

build-aap-mock:
	go build -o bin/$(AAP_MOCK_BINARY_NAME) ./cmd/$(AAP_MOCK_BINARY_NAME)

run:
	go run ./cmd/$(BINARY_NAME)

run-mock-provider:
	go run ./cmd/$(MOCK_BINARY_NAME)

run-aap-mock:
	go run ./cmd/$(AAP_MOCK_BINARY_NAME)

clean:
	rm -rf bin/

fmt:
	gofmt -s -w .

vet:
	go vet ./...

# test/e2e is a separate Go module (REQ-E2E-080) with its own suite that
# needs a live kind cluster (make e2e-test); ginkgo's -r walks the
# filesystem regardless of go.mod boundaries, so it must be skipped
# explicitly here or `make test`/`make check` would fail for anyone without
# a cluster running.
test:
	go run github.com/onsi/ginkgo/v2/ginkgo -r --race --skip-package=test/e2e

test-cover:
	go run github.com/onsi/ginkgo/v2/ginkgo -r --race --cover --skip-package=test/e2e

# test-realbackend-environment-agent runs internal/registration's Tier B
# specs (TC-I-028/029, DD-203) against a REAL, already-running
# environment-agent build — unlike `test`/`test-cover`, which never build or
# start one. Set REAL_ENVIRONMENT_AGENT_URL first (e.g. via the same steps
# as .github/workflows/environment-agent-registration.yaml). Excluded from
# `check` since it needs that external process, not just local fakes.
test-realbackend-environment-agent:
	go run github.com/onsi/ginkgo/v2/ginkgo -tags realbackend --focus "TC-I-02[89]" ./internal/registration/...

lint:
	golangci-lint run ./...

check: fmt vet lint check-aep check-pinned-tags test

tidy:
	go mod tidy

generate-types:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
		--config=api/v1alpha1/types.gen.cfg \
		-o api/v1alpha1/types.gen.go \
		api/v1alpha1/openapi.yaml

generate-spec:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
		--config=api/v1alpha1/spec.gen.cfg \
		-o api/v1alpha1/spec.gen.go \
		api/v1alpha1/openapi.yaml

generate-server:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
		--config=internal/api/server/server.gen.cfg \
		-o internal/api/server/server.gen.go \
		api/v1alpha1/openapi.yaml

generate-client:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
		--config=pkg/client/client.gen.cfg \
		-o pkg/client/client.gen.go \
		api/v1alpha1/openapi.yaml

generate-api: generate-types generate-spec generate-server generate-client

check-generate-api: generate-api
	git diff --exit-code api/ internal/api/server/ pkg/client/ || \
		(echo "Generated files out of sync. Run 'make generate-api'." && exit 1)

# generate-proto regenerates the vendored-minimal OSAC gRPC client (DD-020).
# Requires the buf CLI (https://buf.build/docs/installation).
generate-proto:
	buf generate

check-generate-proto: generate-proto
	git diff --exit-code internal/osacpb/ || \
		(echo "Generated proto files out of sync. Run 'make generate-proto'." && exit 1)

generate: generate-api generate-proto

# Check AEP compliance. Uses npx (not a global install) so this runs with zero
# local setup, matching CI's own `npm install -g @stoplight/spectral-cli` step
# (see .github/workflows/check-aep.yaml) closely enough to catch AEP-133-class
# violations locally instead of only after a PR is pushed.
check-aep:
	npx --yes @stoplight/spectral-cli lint --fail-severity=warn ./api/v1alpha1/openapi.yaml

check-pinned-tags: # TC-TB-040
	./test/e2e/check-pinned-tags.sh

check-container-engine:
	@if [ -z "$(CONTAINER_ENGINE)" ]; then \
		echo "Error: No supported container engine found. Please install podman or docker, or set CONTAINER_ENGINE explicitly." >&2; \
		exit 1; \
	fi

image-build: check-container-engine
	$(CONTAINER_ENGINE) build -t $(CONTAINER_IMAGE_NAME):$(CONTAINER_IMAGE_TAG) .

image-build-mock-provider: check-container-engine
	$(CONTAINER_ENGINE) build -f Containerfile.osac-mock-provider -t $(MOCK_CONTAINER_IMAGE_NAME):$(CONTAINER_IMAGE_TAG) .

image-build-aap-mock: check-container-engine
	$(CONTAINER_ENGINE) build -f Containerfile.osac-aap-mock -t $(AAP_MOCK_CONTAINER_IMAGE_NAME):$(CONTAINER_IMAGE_TAG) .

# e2e targets (Phase 2 of osac-service-provider#17 / FLPATH-4759): local
# helpers for the pieces .github/workflows/e2e.yaml also does. The
# workflow is the canonical, full orchestration (kind create, image
# build+load, control-plane chart install, manifest apply, readiness wait,
# suite run, teardown) — these targets don't reimplement the
# control-plane-chart-fetch-and-install step, since that's CI-specific
# sparse-checkout plumbing with no local-dev equivalent worth duplicating.
KIND_CLUSTER_NAME ?= osac-sp-e2e

e2e-cluster-up:
	kind create cluster --name $(KIND_CLUSTER_NAME) --config test/e2e/kind-config.yaml

e2e-cluster-down:
	kind delete cluster --name $(KIND_CLUSTER_NAME)

e2e-images: check-container-engine
	$(CONTAINER_ENGINE) build -t osac-service-provider:e2e -f Containerfile .
	$(CONTAINER_ENGINE) build -t osac-mock-provider:e2e -f Containerfile.osac-mock-provider .

e2e-load: e2e-images
	kind load docker-image osac-service-provider:e2e --name $(KIND_CLUSTER_NAME)
	kind load docker-image osac-mock-provider:e2e --name $(KIND_CLUSTER_NAME)

e2e-apply:
	kubectl apply -f test/e2e/manifests/

# e2e-test runs the e2e suite (its own Go module, REQ-E2E-080) against an
# already-deployed cluster; set CONTROL_PLANE_URL/OSAC_SP_URL to whatever
# you've port-forwarded/exposed them at.
e2e-test:
	cd test/e2e && go run github.com/onsi/ginkgo/v2/ginkgo -r -v

.PHONY: build build-mock-provider build-aap-mock run run-mock-provider run-aap-mock clean fmt vet test test-cover test-realbackend-environment-agent lint check tidy generate-types generate-spec generate-server generate-client generate-api check-generate-api generate-proto check-generate-proto generate check-aep check-pinned-tags check-container-engine image-build image-build-mock-provider image-build-aap-mock e2e-cluster-up e2e-cluster-down e2e-images e2e-load e2e-apply e2e-test
