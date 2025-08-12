# This Dockerfile creates a production-ready container for a LiveKit agent using Poetry
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

ARG PYTHON_VERSION=3.11.6
FROM python:${PYTHON_VERSION}-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

# Poetry-specific environment variables
# POETRY_HOME: where Poetry itself is installed
# POETRY_VIRTUALENVS_IN_PROJECT: create .venv in project directory
# POETRY_NO_INTERACTION: disable interactive prompts
ENV POETRY_HOME=/opt/poetry \
    POETRY_VIRTUALENVS_IN_PROJECT=true \
    POETRY_NO_INTERACTION=1 \
    POETRY_VERSION=1.8.3

# Add Poetry to PATH
ENV PATH="$POETRY_HOME/bin:$PATH"

# Define the program entrypoint file where your agent is started.
ARG PROGRAM_MAIN="{{.ProgramMain}}"

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

# Install Poetry and build dependencies
# curl is needed to download Poetry installer
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
# Minimal setup with Poetry installation:
RUN apt-get update && \
    apt-get install -y \
    curl \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && curl -sSL https://install.python-poetry.org | python3 - --version $POETRY_VERSION

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /home/appuser

# Copy poetry files first for better Docker layer caching
# If dependencies don't change, Docker can reuse the poetry install layer
COPY pyproject.toml poetry.lock* ./

# Install Python dependencies as root
# --no-root: don't install the project package itself yet
# --only main: only install main dependencies, not dev dependencies
RUN poetry install --no-root --only main

# Copy all application files into the container
# This includes source code, configuration files, etc.
# (Excludes files specified in .dockerignore)
COPY . .

# Install the project itself (if it's a package)
# This step is separate to leverage Docker caching
RUN poetry install --only-root

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /home/appuser

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by pip and Python for caching packages and bytecode
RUN mkdir -p /home/appuser/.cache

# Activate the virtual environment for the runtime
ENV PATH="/home/appuser/.venv/bin:$PATH"

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
RUN python "$PROGRAM_MAIN" download-files

# Expose the healthcheck port
# This allows Docker and orchestration systems to check if the container is healthy
EXPOSE 8081

# Run the application.
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs.
# We use the python from the virtual environment directly
CMD ["python", "{{.ProgramMain}}", "start"]

# === TROUBLESHOOTING POETRY-SPECIFIC ISSUES ===
#
# 1. "poetry.lock not found" warning:
#    - Run `poetry lock` locally before building
#    - Or use `poetry install --no-root` without lock file (less reproducible)
#    - The wildcard in COPY poetry.lock* handles missing lock files
#
# 2. "Package not found" in Poetry:
#    - Ensure package is in pyproject.toml [tool.poetry.dependencies]
#    - Run `poetry add <package>` locally, then rebuild
#    - Check that you're using compatible Python version
#
# 3. Virtual environment issues:
#    - POETRY_VIRTUALENVS_IN_PROJECT=true creates .venv in project
#    - Ensure PATH includes /home/appuser/.venv/bin
#    - Use `poetry run python` if direct python doesn't work
#
# 4. Slow Poetry operations:
#    - Consider using pip export: poetry export -f requirements.txt > requirements.txt
#    - Then use pip install for faster builds
#    - Or increase Poetry installer parallel workers
#
# 5. Development dependencies included:
#    - Use --only main flag to exclude dev dependencies
#    - Or use --without dev,test to exclude specific groups
#    - Check [tool.poetry.group.dev.dependencies] in pyproject.toml
#
# 6. Build cache not working:
#    - Ensure pyproject.toml and poetry.lock are copied before other files
#    - Don't use COPY . . before installing dependencies
#    - Order Dockerfile commands from least to most frequently changed
#
# 7. Permission issues with Poetry:
#    - Install Poetry as root before switching to appuser
#    - Ensure .venv directory is owned by appuser
#    - Set POETRY_CACHE_DIR to user-writable location if needed
#
# For more help: https://python-poetry.org/docs/