# Family HQ — local development, containers, and Argo CD GitOps.
# Recipes match the conventions in Lantern Table and markata.
set shell := ["bash", "-eu", "-o", "pipefail", "-c"]
set dotenv-load := true

registry := "registry.wayl.one"
image_name := "family-dashboard"
engine := env_var_or_default("CONTAINER_ENGINE", "podman")
commit := `git rev-parse HEAD 2>/dev/null || echo development`
tag := env_var_or_default("TAG", "sha-" + commit)
image := registry + "/" + image_name + ":" + tag
namespace := env_var_or_default("NAMESPACE", "family-dashboard")
app := "family-dashboard"

default:
    @just --list

# Run locally. Invalid iCal settings warn + fall back to DEMO; production fails closed.
serve:
    mkdir -p data
    FAMILY_DASHBOARD_DEV_MODE=1 FAMILY_AUTH_MODE=off LISTEN_ADDR="${LISTEN_ADDR:-127.0.0.1:8080}" TASKS_FILE="${TASKS_FILE:-./data/tasks.json}" go run .

# Exercise the QR code/device pairing flow locally without a real hlab-auth login.
# Test bypass is permitted only on loopback and NEVER allowed in production.
auth-demo:
    mkdir -p data
    FAMILY_TEST_PAIRING=1 FAMILY_DASHBOARD_DEV_MODE=1 FAMILY_AUTH_MODE=off LISTEN_ADDR="${LISTEN_ADDR:-127.0.0.1:8080}" TASKS_FILE="${TASKS_FILE:-./data/tasks.json}" go run .

# Import the existing Play Outside weather key from your K3s Secret into local .env.
# Never prints the secret. Requires kubectl permissions and cluster connectivity.
weather-import-play-outside:
    #!/usr/bin/env bash
    set -euo pipefail
    command -v kubectl >/dev/null || { echo 'kubectl is required to read the existing Play Outside Secret' >&2; exit 1; }
    if [[ ! -e .env ]]; then
      cp .env.example .env
    fi
    chmod 600 .env
    if grep -Eq '^PLAY_OUTSIDE_OPEN_WEATHER_API_KEY=' .env; then
      echo 'An existing weather key is already in .env. Remove that line before importing again.' >&2
      exit 1
    fi
    tmp="$(mktemp)"
    trap 'rm -f "$tmp"' EXIT
    chmod 600 "$tmp"
    kubectl -n play-outside get secret play-outside-secret \
      -o jsonpath='{.data.PLAY_OUTSIDE_OPEN_WEATHER_API_KEY}' \
      | base64 --decode > "$tmp"
    test -s "$tmp" || { echo 'The Play Outside Secret has no weather key. Check the namespace and key name.' >&2; exit 1; }
    printf '\nPLAY_OUTSIDE_OPEN_WEATHER_API_KEY=' >> .env
    cat "$tmp" >> .env
    printf '\n' >> .env
    echo 'Imported the existing Play Outside weather key into gitignored .env. Run: just serve'

# Build the local application binary.
build:
    mkdir -p dist
    CGO_ENABLED=0 go build -trimpath -o dist/family-dashboard .

# Run unit and HTTP interaction tests.
test:
    go test ./...

# Run tests with Go's race detector.
test-race:
    go test -race ./...

# Format and check Go code.
fmt:
    gofmt -w main.go main_test.go calendar.go calendar_test.go weather.go weather_test.go auth.go auth_test.go

lint:
    go vet ./...

# Local code validation; no credentials or cluster access required.
check: lint test build

# Print the image tag that the container recipes will use.
image:
    @echo '{{image}}'

# Build the OCI image locally.
container-build:
    {{engine}} build -t '{{image}}' .

# Push the image previously built by container-build.
container-push:
    if [ '{{engine}}' = podman ]; then podman push --tls-verify=false '{{image}}'; else {{engine}} push '{{image}}'; fi

# Build and push in order.
publish: container-build container-push

# Render Kubernetes YAML (requires kubectl, no cluster access).
render:
    kubectl kustomize k8s

# Stage an exact, immutable image tag in this repo's deployment manifest.
pin-image image_ref=image:
    python3 scripts/pin_image.py '{{image_ref}}'

# Build, push, pin, commit/push manifest, sync Argo, and verify rollout.
# Requires a clean, up-to-date main branch, registry auth, Git push, Argo CLI, cluster access.
deploy:
    ./scripts/deploy.sh '{{image}}'

# Manually sync an already pinned image in Argo CD.
sync:
    argocd app sync {{app}}
    kubectl -n {{namespace}} rollout status deployment/{{app}} --timeout=240s

# Local cluster diagnostics.
status:
    kubectl -n {{namespace}} get deploy,po,svc,pvc -o wide

# Follow application logs.
logs:
    kubectl -n {{namespace}} logs -f deployment/{{app}} --tail=100

# Port forward an Argo-managed install for an authenticated local browser.
port-forward:
    kubectl -n {{namespace}} port-forward service/{{app}} 8080:80
