#!/usr/bin/env bash
set -euo pipefail

# Ensure dependencies are tidy
if command -v go >/dev/null 2>&1; then
  go mod tidy
fi

# Build the server binary using the cd-and-build method
mkdir -p bin
cd cmd/server && go build -o ../../bin/server . && cd ../..

chmod +x ./bin/server

echo "Build complete: ./bin/server"
