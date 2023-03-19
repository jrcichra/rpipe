FROM golang:1.20.2-bullseye as builder
WORKDIR /build
COPY . .
RUN ./build.sh
FROM scratch
COPY --from=builder /build/bin/rpiped /rpiped
COPY --from=builder /build/bin/rpipe /rpipe
# other containers will pull in these builds
