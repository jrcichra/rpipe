FROM alpine as rename
WORKDIR /app
COPY target/aarch64-unknown-linux-musl/release/rpiped rpiped-arm64
COPY target/x86_64-unknown-linux-musl/release/rpiped rpiped-amd64
COPY target/aarch64-unknown-linux-musl/release/rpipe rpipe-arm64
COPY target/x86_64-unknown-linux-musl/release/rpipe rpipe-amd64

# busybox allows users to specify this as an initContainer in Kubernetes and copy the desired binary to a shared volume
FROM busybox:1.36.1
ARG TARGETARCH
COPY --from=rename /app/rpiped-$TARGETARCH /app/rpiped
COPY --from=rename /app/rpipe-$TARGETARCH /app/rpipe
ENTRYPOINT [ "/app/rpiped" ]
