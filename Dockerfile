FROM python:3.12-slim

WORKDIR /app

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    procps && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install uv package manager
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

# Attempt to install official Ookla speedtest binary if debian repository is reachable
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "aarch64" ]; then \
        (curl -s https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh | bash && \
         apt-get install -y speedtest && \
         apt-get clean && rm -rf /var/lib/apt/lists/*) || true ; \
    fi

COPY pyproject.toml ./
RUN uv sync --no-dev

COPY monitor/ monitor/
COPY main.py .

RUN mkdir -p /app/data /app/logs
ENV LOG_DIR=/app/logs
ENV DB_PATH=/app/data/network_monitor.db

HEALTHCHECK --interval=60s --timeout=10s --start-period=30s --retries=3 \
    CMD test -f /app/data/.heartbeat || exit 1

CMD ["uv", "run", "python", "main.py"]
