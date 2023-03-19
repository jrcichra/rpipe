# busybox allows users to specify this as an initContainer in Kubernetes and copy the desired binary to a shared volume
FROM busybox:1.36.0
WORKDIR /app
ARG TARGETOS TARGETARCH
COPY bin/rpipe-$TARGETOS-$TARGETARCH /app/rpipe
COPY bin/rpiped-$TARGETOS-$TARGETARCH /app/rpiped
