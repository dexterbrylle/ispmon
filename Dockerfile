# Multi-stage build for ispmon
FROM golang:1.22 AS builder

WORKDIR /src
ENV GOTOOLCHAIN=auto

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /bin/ispmon ./cmd/ispmon

# Final runtime image based on debian:bookworm-slim
FROM debian:bookworm-slim

# Install official Ookla speedtest CLI at build time (fails build if install fails)
RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates && \
    curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | bash && \
    apt-get install -y speedtest && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Run as non-root user
RUN useradd -u 10001 -m -s /bin/bash ispmon && \
    mkdir -p /data && \
    chown -R ispmon:ispmon /data

COPY --from=builder /bin/ispmon /usr/local/bin/ispmon

USER ispmon
WORKDIR /data
VOLUME ["/data"]

ENV DB_PATH=/data/ispmon.db

HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --retries=3 \
    CMD ["ispmon", "healthcheck"]

ENTRYPOINT ["ispmon"]
CMD ["run"]
