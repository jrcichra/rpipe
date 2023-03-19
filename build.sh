#!/bin/bash
set -eou pipefail
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -o bin/rpipe-linux-amd64 cmd/rpipe/*.go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v -o bin/rpipe-linux-arm64 cmd/rpipe/*.go
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -o bin/rpiped-linux-amd64 cmd/rpiped/*.go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v -o bin/rpiped-linux-arm64 cmd/rpiped/*.go
