# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

# UV uses its own optimized base image
ARG PYTHON_VERSION=3.11
FROM ghcr.io/astral-sh/uv:python${PYTHON_VERSION}-bookworm-slim

ENV PYTHONUNBUFFERED=1

# Define the program entrypoint file where your agent is started
ARG PROGRAM_MAIN="{{.ProgramMain}}"

# Create non-privileged user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install system dependencies for Python packages with native extensions
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy dependency files with correct ownership
COPY --chown=appuser:appuser pyproject.toml uv.lock* ./

# Copy all application files
COPY --chown=appuser:appuser . .

# Ensure ownership
RUN chown -R appuser:appuser /app

# Switch to non-privileged user BEFORE installing (UV requires this)
USER appuser

# Install dependencies with UV (creates venv as appuser)
RUN uv sync --locked --python $PYTHON_VERSION

# Pre-download models
RUN uv run "$PROGRAM_MAIN" download-files

# Start the agent
CMD ["uv", "run", "{{.ProgramMain}}", "start"]