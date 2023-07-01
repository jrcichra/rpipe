FROM alpine as rename
WORKDIR /app
COPY target/aarch64-unknown-linux-gnu/release/rpiped rpiped-arm64
COPY target/x86_64-unknown-linux-gnu/release/rpiped rpiped-amd64
COPY target/aarch64-unknown-linux-gnu/release/rpipe rpipe-arm64
COPY target/x86_64-unknown-linux-gnu/release/rpipe rpipe-amd64

FROM gcr.io/distroless/static-debian11:nonroot
ARG TARGETARCH
COPY --from=rename /app/rpiped-$TARGETARCH /app/rpiped
COPY --from=rename /app/rpipe-$TARGETARCH /app/rpipe
ENTRYPOINT [ "/app/rpiped" ]
