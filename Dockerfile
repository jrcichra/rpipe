FROM golang:1.20.2-bullseye as builder
WORKDIR /build
COPY . .
RUN ./build.sh
# other containers will pull in these builds
