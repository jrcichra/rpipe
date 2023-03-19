FROM scratch
WORKDIR /app
ARG TARGETOS TARGETARCH
COPY bin/rpipe-$TARGETOS-$TARGETARCH /app/rpipe
COPY bin/rpiped-$TARGETOS-$TARGETARCH /app/rpiped
# other containers will pull in these builds
