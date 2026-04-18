#!/usr/bin/env bash
# Run `ions` inside a Linux Docker container that has access to the host's
# Docker daemon. This is how we test ions against real workflows: the
# container is Linux-native, so runner container support works and the
# host-fallback path isn't triggered. Child containers (job containers,
# service containers) run as siblings on the host docker so they can
# mount workspace paths that resolve identically on both sides.
#
# Usage:
#   scripts/ions-in-docker.sh <repo-path> <ions args...>
#
# Example:
#   scripts/ions-in-docker.sh ../base-monorepo run .github/workflows/trufflehog.yml --event pull_request

set -euo pipefail

if [ "$#" -lt 1 ]; then
    echo "usage: $0 <repo-path> [ions args...]" >&2
    exit 1
fi

repo="$(cd "$1" && pwd)"
shift

ions_root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$ions_root/bin/ions-linux"

# Build a Linux ions binary via cross-compilation. Fast because most
# deps are pure Go.
mkdir -p "$ions_root/bin"
(cd "$ions_root" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$bin" ./cmd/ions)

# Build (or refresh) the runtime image once. Cheap after the first build.
docker build -q -f "$ions_root/Dockerfile.dev" -t ions-dev "$ions_root" >/dev/null

# Run ions inside the container with:
# - host docker socket so ions can launch sibling containers
# - the same absolute paths on both sides so bind-mounts resolve
#   correctly when the runner asks the host docker to bind-mount them
#   into a new sibling container. HOME is pointed at /tmp/ions-state so
#   ions's state dir lives at an identical host+container path.
state="/tmp/ions-state"
mkdir -p "$state"

# Use a fixed broker port so we can -p publish it on the host. Sibling
# job containers reach the broker via host.docker.internal:<port>, which
# routes to the host where the port is published, which forwards into
# the ions-dev container. A random port would require us to know it
# before starting the container — fixed is simpler.
broker_port="${IONS_BROKER_PORT:-19999}"

# If the invoker passed a bare subcommand like "run ..." with no broker
# networking / runner-image flags, inject sensible defaults for the
# in-docker test environment. Ubuntu-* jobs default to catthehacker's
# image because stock ubuntu:24.04 lacks git/docker/curl/python — most
# real workflows fail inside it.
default_runner_image="${IONS_RUNNER_IMAGE:-ghcr.io/catthehacker/ubuntu:act-24.04}"

args=("$@")
if [ "${args[0]:-}" = "run" ]; then
    needs_bind=1
    needs_host=1
    needs_token=1
    needs_image=1
    for a in "${args[@]}"; do
        case "$a" in
            --broker-bind|--broker-bind=*)     needs_bind=0 ;;
            --broker-host|--broker-host=*)     needs_host=0 ;;
            --github-token|--github-token=*)   needs_token=0 ;;
            --runner-image|--runner-image=*)   needs_image=0 ;;
        esac
    done
    injected=()
    [ "$needs_bind" = 1 ]  && injected+=("--broker-bind=0.0.0.0:$broker_port")
    [ "$needs_host" = 1 ]  && injected+=("--broker-host=host.docker.internal")
    [ "$needs_image" = 1 ] && injected+=("--runner-image=$default_runner_image")
    if [ "$needs_token" = 1 ] && [ -n "${IONS_GITHUB_TOKEN:-}" ]; then
        injected+=(--github-token="$IONS_GITHUB_TOKEN")
    fi
    args=("${args[0]}" "${injected[@]}" "${args[@]:1}")
fi

exec docker run --rm -i \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$bin":/usr/local/bin/ions:ro \
    -v "$repo":"$repo" \
    -v "$state":"$state" \
    -e HOME="$state" \
    -p "$broker_port:$broker_port" \
    --add-host=host.docker.internal:host-gateway \
    -w "$repo" \
    --env IONS_HOST_REPO="$repo" \
    ions-dev "${args[@]}"
