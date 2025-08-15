# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

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

# Copy and install dependencies
COPY requirements.txt .
RUN python -m pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

# Create cache directory for the user
RUN mkdir -p /app/.cache

# Pre-download models
RUN python "$PROGRAM_MAIN" download-files

# Start the agent
CMD ["python", "{{.ProgramMain}}", "start"]