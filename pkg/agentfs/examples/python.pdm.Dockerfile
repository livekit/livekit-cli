# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1

# Don't use venv (use system Python)
ENV PDM_USE_VENV=false \
    PDM_IGNORE_SAVED_PYTHON=true

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

# Install PDM and system dependencies
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir pdm

WORKDIR /app

# Copy dependency files
COPY pyproject.toml pdm.lock* ./

# Install production dependencies without the project
RUN pdm install --prod --no-self

# Copy application code
COPY . .

# Install the project
RUN pdm install --prod --no-editable

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

# Create cache directory for the user
RUN mkdir -p /app/.cache

# Pre-download models
RUN pdm run python "$PROGRAM_MAIN" download-files

# Start the agent
CMD ["pdm", "run", "python", "{{.ProgramMain}}", "start"]