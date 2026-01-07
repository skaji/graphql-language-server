#!/bin/bash

set euxo -pipefail

golangci-lint run ./...

GOPLS_OUT=$(git ls-files '*.go' | xargs gopls check -severity hint)
echo "$GOPLS_OUT"
if [[ -n $GOPLS_OUT ]]; then
  exit 1
fi
