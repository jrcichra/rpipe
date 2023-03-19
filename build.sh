#!/bin/bash
set -eou pipefail
mkdir -p bin
CGO_ENABLED=0 go build -v -o bin/rpipe cmd/rpipe/*.go
CGO_ENABLED=0 go build -v -o bin/rpiped cmd/rpiped/*.go
