FROM golang:1.20.2-bullseye as builder
WORKDIR /build
COPY . .
RUN ./build.sh
FROM scratch
COPY --from=builder /bin/rpiped /rpiped
COPY --from=builder /bin/rpipe /rpipe
# other containers will pull in these builds
