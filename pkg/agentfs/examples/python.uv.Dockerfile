# This sample Dockerfile creates a production-ready container for a LiveKit voice AI agent
# syntax=docker/dockerfile:1

# Use the official UV Python base image with Python 3.11 on Debian Bookworm
# UV is a fast Python package manager that provides better performance than pip
# We use the slim variant to keep the image size smaller while still having essential tools
FROM ghcr.io/astral-sh/uv:python3.11-bookworm-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

# Create a non-privileged user that the app will run under.
# See https://docs.docker.com/develop/develop-images/dockerfile_best-practices/#user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/home/appuser" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install build dependencies required for Python packages with native extensions
# gcc: C compiler needed for building Python packages with C extensions
# python3-dev: Python development headers needed for compilation
# We clean up the apt cache after installation to keep the image size down
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /home/appuser

# Copy all application files into the container
# This includes source code, configuration files, and dependency specifications
# (Excludes files specified in .dockerignore)
COPY . .

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /home/appuser

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by UV and Python for caching packages and bytecode
RUN mkdir -p /home/appuser/.cache

# Install Python dependencies using UV's lock file
# --locked ensures we use exact versions from uv.lock for reproducible builds
# This creates a virtual environment and installs all dependencies
# Ensure your uv.lock file is checked in for consistency across environments
RUN uv sync --locked

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
RUN uv run src/agent.py download-files

# Expose the healthcheck port
# This allows Docker and orchestration systems to check if the container is healthy
EXPOSE 8081

# Run the application using UV
# UV will activate the virtual environment and run the agent
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs
CMD ["uv", "run", "src/agent.py", "start"]
