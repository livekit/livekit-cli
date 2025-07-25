# This is an example Dockerfile that builds a minimal container for running LK Agents
# syntax=docker/dockerfile:1
ARG PYTHON_VERSION=3.11.6
FROM python:${PYTHON_VERSION}-slim

# Keeps Python from buffering stdout and stderr to avoid situations where
# the application crashes without emitting any logs due to buffering.
ENV PYTHONUNBUFFERED=1

# Define the program entrypoint file where your agent is started
ARG PROGRAM_MAIN="src/agent.py"

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

WORKDIR /home/appuser

COPY requirements.txt .
RUN python -m pip install --user --no-cache-dir -r requirements.txt

COPY . .

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /home/appuser

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Create a cache directory for the user
# This is used by pip and Python for caching packages and bytecode
RUN mkdir -p /home/appuser/.cache

# ensure that any dependent models are downloaded at build-time
RUN python "$PROGRAM_MAIN" download-files

# expose healthcheck port
EXPOSE 8081

# Run the application.
CMD ["python", "$PROGRAM_MAIN", "start"]

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
# 3. "requirements.txt not found" or install errors:
#    - Ensure requirements.txt is in your project root
#    - Check that requirements.txt is not in .dockerignore
#    - Pin package versions for reproducible builds
#
# 4. Large image sizes:
#    - Consider multi-stage builds for production
#    - Remove unnecessary packages after installation
#    - Use .dockerignore to exclude large files
#
# 5. Slow builds:
#    - Consider switching to UV for faster dependency resolution
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
