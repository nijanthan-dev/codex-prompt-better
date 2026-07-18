#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly root
cd "${root}"

test -z "$(gofmt -l .)"
go run ./scripts/generate-erd.go
go run ./scripts/validate_patterns.go
go test -count=1 ./...
go vet ./...
go run ./scripts/validate_contracts.go
