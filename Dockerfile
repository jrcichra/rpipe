FROM scratch
WORKDIR /app
COPY bin/* .
# other containers will pull in these builds
