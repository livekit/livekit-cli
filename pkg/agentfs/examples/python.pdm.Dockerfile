# This Dockerfile creates a production-ready container for a LiveKit agent using PDM
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
# COPY --from=builder /home/appuser/.venv /home/appuser/.venv
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

# PDM-specific environment variables
# PDM_USE_VENV: Use virtual environment
# PDM_IGNORE_SAVED_PYTHON: Don't use saved Python interpreter
ENV PDM_USE_VENV=true \
    PDM_IGNORE_SAVED_PYTHON=true

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

# Install PDM and build dependencies
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
# Minimal setup with PDM installation:
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir pdm

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /home/appuser

# Copy PDM files first for better Docker layer caching
# If dependencies don't change, Docker can reuse the pdm install layer
COPY pyproject.toml pdm.lock* ./

# Install Python dependencies as root
# --prod: only install production dependencies, not dev dependencies
# --no-self: don't install the project package itself yet
RUN pdm install --prod --no-self

# Copy all application files into the container
# This includes source code, configuration files, etc.
# (Excludes files specified in .dockerignore)
COPY . .

# Install the project itself (if it's a package)
# This step is separate to leverage Docker caching
RUN pdm install --prod --no-editable

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /home/appuser

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by pip and Python for caching packages and bytecode
RUN mkdir -p /home/appuser/.cache

# Set up the PATH to include PDM's virtual environment
# PDM creates .venv in the project directory by default
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
# We use the python from PDM's virtual environment
CMD ["python", "{{.ProgramMain}}", "start"]

# === TROUBLESHOOTING PDM-SPECIFIC ISSUES ===
#
# 1. "pdm.lock not found" warning:
#    - Run `pdm lock` locally before building
#    - Or use `pdm install --prod` without lock file (less reproducible)
#    - The wildcard in COPY pdm.lock* handles missing lock files
#
# 2. "pyproject.toml not found":
#    - PDM requires pyproject.toml with project metadata
#    - Ensure [project] section exists with name and dependencies
#    - Use `pdm init` locally to create a proper project structure
#
# 3. Virtual environment issues:
#    - PDM creates .venv in project directory by default
#    - Set PDM_USE_VENV=true to ensure venv is used
#    - Ensure PATH includes /home/appuser/.venv/bin
#
# 4. Dependencies not installed:
#    - Ensure dependencies are in [project.dependencies] section
#    - Dev dependencies go in [tool.pdm.dev-dependencies]
#    - Use --prod flag to exclude dev dependencies in production
#
# 5. Slow builds:
#    - PDM resolves and downloads dependencies fresh
#    - Use pdm.lock for reproducible, faster builds
#    - Consider using `pdm export` to generate requirements.txt
#
# 6. Python version conflicts:
#    - PDM respects requires-python in pyproject.toml
#    - Ensure Docker Python version matches project requirements
#    - Use PDM_IGNORE_SAVED_PYTHON to ignore local Python path
#
# 7. Permission issues:
#    - Install PDM and dependencies as root first
#    - Then chown to appuser before switching users
#    - Ensure .venv directory is owned by appuser
#
# 8. Group dependencies:
#    - PDM supports dependency groups like Poetry
#    - Use `pdm install -G groupname` to install specific groups
#    - Production builds should use --prod to exclude dev dependencies
#
# For more help: https://pdm.fming.dev/