# This Dockerfile creates a production-ready container for a LiveKit agent using UV
# UV is a fast Python package manager that provides better performance than pip
# syntax=docker/dockerfile:1
#
# === MULTI-STAGE BUILD OPTIMIZATION ===
# For smaller production images, consider using a multi-stage build:
# Stage 1: Build dependencies and compile packages
# Stage 2: Copy only the compiled packages to a clean runtime image
#
# Example multi-stage build structure:
# FROM ghcr.io/astral-sh/uv:python3.11-bookworm AS builder
# [install build tools, compile packages]
# FROM python:3.11-slim AS runtime
# COPY --from=builder /path/to/compiled/packages ./
# [runtime setup only]
#
# Benefits: 30-50% smaller final image size
# Trade-offs: Longer build time, more complex debugging
# Use when: Image size is critical (e.g., serverless, edge deployment)

# Use the official UV Python base image with Python 3.11 on Debian Bookworm
# We use the slim variant to keep the image size smaller while still having essential tools
ARG PYTHON_VERSION=3.11
FROM ghcr.io/astral-sh/uv:python${PYTHON_VERSION}-bookworm-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

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

# Install build dependencies required for Python packages with native extensions
#
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
# === Examples for Common Use Cases ===
#
# Audio Processing Agent:
# RUN apt-get update && \
#     apt-get install -y \
#     gcc python3-dev \
#     libasound2-dev libportaudio2 libsndfile1-dev \
#     ffmpeg \
#     && rm -rf /var/lib/apt/lists/*
#
# Computer Vision Agent:
# RUN apt-get update && \
#     apt-get install -y \
#     gcc python3-dev \
#     libopencv-dev libjpeg-dev libpng-dev libwebp-dev \
#     && rm -rf /var/lib/apt/lists/*
#
# Machine Learning Agent:
# RUN apt-get update && \
#     apt-get install -y \
#     gcc g++ gfortran python3-dev \
#     libblas-dev liblapack-dev libatlas-base-dev \
#     && rm -rf /var/lib/apt/lists/*
#
# === SPECIAL CASE: PyAudio Installation Guide ===
# PyAudio is a common audio library that requires special attention.
# If you need PyAudio, use this complete setup:
#
# RUN apt-get update && \
#     apt-get install -y \
#     gcc python3-dev \
#     libasound2-dev libportaudio2 portaudio19-dev \
#     && rm -rf /var/lib/apt/lists/*
#
# Important notes for PyAudio:
# 1. Install system packages BEFORE installing Python packages
# 2. Use portaudio19-dev (not just libportaudio2) for full compatibility
# 3. Some systems may also need: libportaudiocpp0 libportaudio0
# 4. For production use, consider using sounddevice instead of pyaudio
#    (sounddevice is more modern and has better error handling)
#
# Alternative for audio: Use soundfile + sounddevice instead of pyaudio:
# RUN apt-get update && \
#     apt-get install -y \
#     gcc python3-dev \
#     libasound2-dev libsndfile1-dev \
#     && rm -rf /var/lib/apt/lists/*
#
# Then in your requirements: soundfile sounddevice (instead of pyaudio)
#
# Minimal setup (works for most pure Python packages):
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

# Set the working directory to the user's home directory
# This is where our application code will live
WORKDIR /app

# Copy dependency files first for better layer caching
# We copy as root first to ensure files are accessible
COPY --chown=appuser:appuser pyproject.toml uv.lock* ./

# Copy all application files into the container
# This includes source code, configuration files, and dependency specifications
# (Excludes files specified in .dockerignore)
COPY --chown=appuser:appuser . .

# Ensure the app directory is owned by the appuser
RUN chown -R appuser:appuser /app

# Switch to the non-privileged user before installing dependencies
# This ensures the virtual environment is created with correct ownership
USER appuser

# Install Python dependencies using UV's lock file
# --locked ensures we use exact versions from uv.lock for reproducible builds
# This creates a virtual environment owned by appuser
# Ensure your uv.lock file is checked in for consistency across environments
RUN uv sync --locked --python $PYTHON_VERSION

# Create a cache directory for the user
# This is used by UV and Python for caching packages and bytecode
RUN mkdir -p /app/.cache

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
RUN uv run "$PROGRAM_MAIN" download-files

# Run the application using UV
# UV will activate the virtual environment and run the agent.
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs.
CMD ["uv", "run", "{{.ProgramMain}}", "start"]

# === TROUBLESHOOTING COMMON BUILD ISSUES ===
#
# 1. "Package not found" or compilation errors:
#    - Check that required system packages are installed (see examples above)
#    - Ensure packages are installed BEFORE Python package installation
#    - For C extensions: you need gcc and python3-dev
#
# 2. "Permission denied" errors:
#    - Verify USER appuser comes after chown commands
#    - Check that working directory is /home/appuser
#    - Make sure all files are owned by appuser:appuser
#
# 3. "uv.lock not found" or sync errors:
#    - Ensure uv.lock is in your project root
#    - Run 'uv lock' locally before building
#    - Check that uv.lock is not in .dockerignore
#
# 4. Large image sizes:
#    - Consider multi-stage builds (see documentation above)
#    - Remove unnecessary packages after installation
#    - Use .dockerignore to exclude large files
#
# 5. Slow builds:
#    - UV provides faster dependency resolution than pip
#    - Use Docker BuildKit for better caching
#    - Order Dockerfile commands from least to most frequently changed
#
# 6. Runtime issues:
#    - Check healthcheck endpoint (port 8081)
#    - Verify environment variables are set
#    - Ensure agent can connect to LiveKit server
#    - Check that required models/files are downloaded
#
# 7. Audio/video issues:
#    - Install required system libraries (see PyAudio guide above)
#    - Test with minimal audio setup first
#    - Consider using sounddevice instead of pyaudio
#
# For more help: https://docs.livekit.io/agents/
# For build options and troubleshooting: https://docs.livekit.io/agents/ops/deployment/cloud/build