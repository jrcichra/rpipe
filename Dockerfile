FROM golang:1.20.2-bullseye as builder
WORKDIR /build
RUN mkdir bin && go build -v -o rpipe cmd/rpipe/*.go && go build -v -o rpiped cmd/rpiped/*.go
# other containers will pull in these builds
