#!/bin/bash
set -euo pipefail

# Build the integration test image.
echo "Building integration test image..."
docker build -f Dockerfile.integration -t ions-integration .

# Run integration tests.
# Mount the Docker socket so service container tests can talk to Docker.
# Pass through any extra args (e.g. -run TestIntegration_HelloWorld).
echo "Running integration tests..."
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    ions-integration "$@"
