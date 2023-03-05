#!/bin/bash
set -eou pipefail
mkdir -p bin
go build -v -o bin/rpipe cmd/rpipe/*.go
go build -v -o bin/rpiped cmd/rpiped/*.go
