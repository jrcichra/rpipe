FROM alpine as rename
WORKDIR /app
COPY target/aarch64-unknown-linux-gnu/release/rpiped rpiped-arm64
COPY target/x86_64-unknown-linux-gnu/release/rpiped rpiped-amd64

FROM gcr.io/distroless/static-debian11:nonroot
COPY --from=rename /app/bitwarden-secrets-operator-$TARGETARCH /app/rpiped
ENTRYPOINT [ "/app/rpiped" ]
