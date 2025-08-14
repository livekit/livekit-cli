# This Dockerfile creates a production-ready container for a LiveKit agent using Hatch
# syntax=docker/dockerfile:1
#
# === MULTI-STAGE BUILD OPTIMIZATION ===
# For smaller production images, consider using a multi-stage build:
# Stage 1: Build dependencies and compile packages
# Stage 2: Copy only the compiled packages to a clean runtime image
#
# Example multi-stage build structure:
# FROM python:3.11-slim AS builder
# [install build tools, compile packages]
# FROM python:3.11-slim AS runtime
# COPY --from=builder /home/appuser/.local /home/appuser/.local
# [runtime setup only]
#
# Benefits: 30-50% smaller final image size
# Trade-offs: Longer build time, more complex debugging
# Use when: Image size is critical (e.g., serverless, edge deployment)

ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

# Hatch-specific environment variables
# HATCH_ENV_TYPE_VIRTUAL_PATH: Use a specific path for virtual environments
ENV HATCH_ENV_TYPE_VIRTUAL_PATH=/app/.venv

# Define the program entrypoint file where your agent is started.
ARG PROGRAM_MAIN="{{.ProgramMain}}"

# Create a non-privileged user that the app will run under.
# See https://docs.docker.com/develop/develop-images/dockerfile_best-practices/#user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install Hatch and build dependencies
# Common system packages you might need (uncomment and modify as needed):
#
# === Core Build Tools ===
# - gcc/g++: C/C++ compilers for building packages with native extensions
# - python3-dev: Python development headers needed for compilation
# - build-essential: Essential build tools (includes gcc, make, etc.)
# - pkg-config: Tool for managing library compilation/linking flags
#
# === Audio Processing ===
# For audio agents (pyaudio, soundfile, librosa):
# - libasound2-dev: ALSA development headers
# - libportaudio2: Cross-platform audio I/O library
# - libsndfile1-dev: Library for reading/writing audio files
# - ffmpeg: Audio/video processing (for format conversion)
#
# === Computer Vision ===
# For image/video processing (opencv, pillow):
# - libopencv-dev: OpenCV development headers
# - libjpeg-dev: JPEG image format support
# - libpng-dev: PNG image format support
# - libwebp-dev: WebP image format support
# - libtiff5-dev: TIFF image format support
#
# === Machine Learning ===
# For ML/AI packages (scipy, numpy, scikit-learn):
# - libblas-dev: Basic Linear Algebra Subprograms
# - liblapack-dev: Linear Algebra Package
# - libatlas-base-dev: Automatically Tuned Linear Algebra Software
# - gfortran: Fortran compiler (needed for some numerical libraries)
#
# === Database & Networking ===
# - libpq-dev: PostgreSQL development headers
# - libmysqlclient-dev: MySQL development headers
# - libssl-dev: SSL/TLS support for cryptographic packages
# - libffi-dev: Foreign Function Interface library for cffi
# - libcurl4-openssl-dev: HTTP client library
#
# Minimal setup with Hatch installation:
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir hatch

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /app

# Copy project files first for better Docker layer caching
# Hatch needs pyproject.toml to understand the project
COPY pyproject.toml ./
COPY hatch.toml* ./

# Create virtual environment and install dependencies as root
# Hatch will automatically handle dependency resolution
RUN hatch env create default

# Copy all application files into the container
# This includes source code, configuration files, etc.
# (Excludes files specified in .dockerignore)
COPY . .

# Install the project in the hatch environment
# This ensures all dependencies are properly installed
RUN hatch run python -m pip install -e .

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /app

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by pip and Python for caching packages and bytecode
RUN mkdir -p /app/.cache

# Set up the PATH to include Hatch's virtual environment
ENV PATH="/app/.venv/bin:$PATH"

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
# We use hatch run to ensure the command runs in the correct environment
RUN hatch run python "$PROGRAM_MAIN" download-files

# Run the application.
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs.
# We use hatch run to ensure we're using the correct environment
CMD ["hatch", "run", "python", "{{.ProgramMain}}", "start"]

# === TROUBLESHOOTING HATCH-SPECIFIC ISSUES ===
#
# 1. "pyproject.toml not found":
#    - Hatch requires pyproject.toml with [build-system] section
#    - Ensure pyproject.toml defines project metadata
#    - Use `hatch new` locally to create a proper project structure
#
# 2. Environment issues:
#    - Hatch creates isolated environments by default
#    - Check HATCH_ENV_TYPE_VIRTUAL_PATH is set correctly
#    - Use `hatch env show` to debug environment configuration
#
# 3. Dependencies not installed:
#    - Ensure dependencies are in [project.dependencies] section
#    - Optional dependencies go in [project.optional-dependencies]
#    - Use `hatch dep show` to verify dependency tree
#
# 4. Slow builds:
#    - Hatch downloads and builds dependencies fresh
#    - Consider using pip-compile for locked requirements
#    - Cache /app/.cache between builds if possible
#
# 5. Version conflicts:
#    - Hatch uses the latest resolver by default
#    - Pin specific versions in pyproject.toml if needed
#    - Use `hatch dep sync` to update lock file
#
# 6. Build system issues:
#    - Hatch supports multiple build backends
#    - Default is hatchling, but setuptools also works
#    - Check [build-system] section in pyproject.toml
#
# 7. Permission issues:
#    - Ensure virtual environment is created as root first
#    - Then chown to appuser before switching users
#    - HATCH_ENV_TYPE_VIRTUAL_PATH must be writable by appuser
#
# For more help: https://hatch.pypa.io/latest/
# For build options and troubleshooting: https://docs.livekit.io/agents/ops/deployment/cloud/build