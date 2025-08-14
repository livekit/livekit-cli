# This Dockerfile creates a production-ready container for a LiveKit agent using Pipenv
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

# Pipenv-specific environment variables
# PIPENV_VENV_IN_PROJECT: Create .venv in project directory
# PIPENV_IGNORE_VIRTUALENVS: Ignore any existing virtual environments
ENV PIPENV_VENV_IN_PROJECT=1 \
    PIPENV_IGNORE_VIRTUALENVS=1

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

# Install Pipenv and build dependencies
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
# Minimal setup with Pipenv installation:
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir pipenv

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /app

# Copy Pipenv files first for better Docker layer caching
# If dependencies don't change, Docker can reuse the pipenv install layer
COPY Pipfile Pipfile.lock* ./

# Install Python dependencies as root
# --deploy: Abort if the Pipfile.lock is out-of-date
# --ignore-pipfile: Use the Pipfile.lock for installation (if it exists)
# Without lock file, remove --deploy flag
RUN if [ -f Pipfile.lock ]; then \
        pipenv install --deploy --ignore-pipfile; \
    else \
        pipenv install --skip-lock; \
    fi

# Copy all application files into the container
# This includes source code, configuration files, etc.
# (Excludes files specified in .dockerignore)
COPY . .

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /app

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by pip and Python for caching packages and bytecode
RUN mkdir -p /app/.cache

# Set up the PATH to include Pipenv's virtual environment
# Pipenv creates .venv in the project directory
ENV PATH="/app/.venv/bin:$PATH"

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
RUN python "$PROGRAM_MAIN" download-files

# Run the application.
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs.
# We use the python from Pipenv's virtual environment
CMD ["python", "{{.ProgramMain}}", "start"]

# === TROUBLESHOOTING PIPENV-SPECIFIC ISSUES ===
#
# 1. "Pipfile.lock not found" warning:
#    - Run `pipenv lock` locally before building
#    - Or use `pipenv install --skip-lock` (less reproducible)
#    - The wildcard in COPY Pipfile.lock* handles missing lock files
#
# 2. "Package not found" in Pipenv:
#    - Ensure package is in [packages] section of Pipfile
#    - Run `pipenv install <package>` locally, then rebuild
#    - Check Python version compatibility in Pipfile
#
# 3. Virtual environment issues:
#    - PIPENV_VENV_IN_PROJECT=1 creates .venv in project
#    - Ensure PATH includes /app/.venv/bin
#    - Use `pipenv run python` if direct python doesn't work
#
# 4. Slow Pipenv operations:
#    - Pipenv can be slow, especially without a lock file
#    - Always use Pipfile.lock for faster builds
#    - Consider switching to Poetry or PDM for better performance
#
# 5. Version conflicts:
#    - Pipenv respects python_version in Pipfile
#    - Ensure Docker Python version matches Pipfile requirement
#    - Use --python flag when creating Pipfile locally
#
# 6. Development dependencies included:
#    - Pipenv separates [packages] and [dev-packages]
#    - Docker build only installs [packages] by default
#    - Use `pipenv install --dev` if dev packages needed
#
# 7. Permission issues:
#    - Install Pipenv and dependencies as root first
#    - Then chown to appuser before switching users
#    - Ensure .venv directory is owned by appuser
#
# 8. Lock file out of sync:
#    - --deploy flag ensures Pipfile.lock matches Pipfile
#    - Remove --deploy if you want to ignore lock file issues
#    - Regenerate lock with `pipenv lock` locally
#
# For more help: https://pipenv.pypa.io/
# For build options and troubleshooting: https://docs.livekit.io/agents/ops/deployment/cloud/build