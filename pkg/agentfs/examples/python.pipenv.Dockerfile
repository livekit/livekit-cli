# syntax=docker/dockerfile:1
# For detailed documentation and guides, see:
# https://github.com/livekit/livekit-cli/blob/main/pkg/agentfs/examples/README.md
# For more help: https://docs.livekit.io/agents/
# For help with building and deployment: https://docs.livekit.io/agents/ops/deployment/cloud/build

ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1

# Create .venv in project directory
ENV PIPENV_VENV_IN_PROJECT=1 \
    PIPENV_IGNORE_VIRTUALENVS=1

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

# Install Pipenv and system dependencies
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir pipenv

WORKDIR /app

# Copy dependency files
COPY Pipfile Pipfile.lock* ./

# Install dependencies
RUN if [ -f Pipfile.lock ]; then \
        pipenv install --deploy --ignore-pipfile; \
    else \
        pipenv install --skip-lock; \
    fi

# Copy application code
COPY . .

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

# Set PATH to include virtual environment
ENV PATH="/app/.venv/bin:$PATH"

# Create cache directory for the user
RUN mkdir -p /app/.cache

# Pre-download models
RUN python "$PROGRAM_MAIN" download-files

# Start the agent
CMD ["python", "{{.ProgramMain}}", "start"]