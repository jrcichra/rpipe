#!/bin/bash
set -eou pipefail
mkdir -p bin
go build -race -v -o bin/rpipe cmd/rpipe/*.go
go build -race -v -o bin/rpiped cmd/rpiped/*.go
