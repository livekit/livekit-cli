# LiveKit Agent Dockerfile Reference

A comprehensive guide for containerizing LiveKit agents across different package managers and runtime environments.

## Quick Start: Testing Your Build Locally

To test your Dockerfile during development:

```bash
# Build the Docker image
docker build -t my-agent .

# Run the agent locally (replace with your actual environment variables)
docker run --rm \
  -e LIVEKIT_URL=wss://your-livekit-server.com \
  -e LIVEKIT_API_KEY=your-api-key \
  -e LIVEKIT_API_SECRET=your-api-secret \
  my-agent

# For debugging, run with interactive shell
docker run --rm -it --entrypoint /bin/bash my-agent
```

## Table of Contents

- [Node.js Package Managers](#nodejs-package-managers)
  - [npm](#npm)
  - [pnpm](#pnpm)
  - [Yarn Classic (v1)](#yarn-classic-v1)
  - [Yarn Berry (v2+)](#yarn-berry-v2)
  - [Bun](#bun)
- [Python Package Managers](#python-package-managers)
  - [pip](#pip)
  - [UV](#uv)
  - [Poetry](#poetry)
  - [Pipenv](#pipenv)
  - [PDM](#pdm)
  - [Hatch](#hatch)
- [Common Configurations](#common-configurations)
  - [Security Configuration](#security-configuration)
  - [Environment Variables](#environment-variables)
  - [Native Module Dependencies](#native-module-dependencies)
  - [System Dependencies](#system-dependencies)
- [Build Optimization](#build-optimization)
- [Runtime Configuration](#runtime-configuration)
- [Converting Between Project Types](#converting-between-project-types)
- [Troubleshooting](#troubleshooting)

## Node.js Package Managers

### General Build Process

All Node.js package managers follow a similar three-stage pattern:

**Build Stage:**
- Install CA certificates for HTTPS package downloads
- Copy package files and/or source files (order varies by package manager)
- Install ALL dependencies (including dev) for TypeScript compilation
- Copy remaining source files (if not already copied)
- Build/compile TypeScript, run webpack, etc.
- Prune to production dependencies only (remove dev/build dependencies)

**Runtime Stage:**
- Set NODE_ENV=production
- Create non-privileged user (appuser)
- Copy built application and SSL certificates from build stage
- Fix permissions for /app directory
- Switch to appuser
- Run the application with CMD

### npm

[NPM Dockerfile](node.npm.Dockerfile) | [NPM Dockerignore](node.npm.dockerignore)

**Package files:** `package.json`, `package-lock.json`

```dockerfile
# === BASE STAGE ===
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base
WORKDIR /app

# === BUILD STAGE ===
FROM base AS build

# Install CA certificates for HTTPS connections
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy package files first (layer caching)
COPY package*.json ./

# Install ALL dependencies (including dev) for TypeScript compilation
RUN npm ci

# Copy source code
COPY . .

# Build TypeScript to JavaScript
RUN npm run build

# Remove dev dependencies after build
RUN npm prune --production

# === RUNTIME STAGE ===
FROM base

# Set production environment
ENV NODE_ENV=production

# Create non-privileged user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Copy built application from build stage
COPY --from=build /app /app

# Copy SSL certificates for HTTPS
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Set ownership and switch user
RUN chown -R appuser:appuser /app
USER appuser

CMD ["node", "agent.js", "start"]
```

**Key characteristics:**
- Install all dependencies initially (npm ci without --omit=dev) to support TypeScript compilation
- Build TypeScript files
- Prune dev dependencies after building
- NODE_ENV=production set in runtime stage

### pnpm

[PNPM Dockerfile](node.pnpm.Dockerfile) | [PNPM Dockerignore](node.pnpm.dockerignore)

**Package files:** `package.json`, `pnpm-lock.yaml`

```dockerfile
# === BASE STAGE ===
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base
WORKDIR /app

# Install pnpm globally
RUN npm install -g pnpm@9.15.9
ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"

# === BUILD STAGE ===
FROM base AS build

RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy all files (pnpm needs the full context)
COPY --link . .

# Install dependencies with frozen lockfile
RUN pnpm install --frozen-lockfile

# Build the application
RUN pnpm run build

# Remove dev dependencies
RUN pnpm prune --prod

# === RUNTIME STAGE ===
FROM base
ENV NODE_ENV=production

# Create non-privileged user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

COPY --from=build /app /app
COPY --from=build /etc/ssl/certs /etc/ssl/certs

RUN chown -R appuser:appuser /app
USER appuser

CMD ["node", "agent.js", "start"]
```

**Key characteristics:**
- pnpm must be installed globally in base stage
- Uses `--link` flag when copying to preserve symlinks
- Copies all files at once (pnpm handles caching internally)
- Initially installs all dependencies, then builds, then prunes for prod

### Yarn Classic (v1)

[Yarn Dockerfile](node.yarn.Dockerfile) | [Yarn Dockerignore](node.yarn.dockerignore)

**Package files:** `package.json`, `yarn.lock`

```dockerfile
# === BASE STAGE ===
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base
WORKDIR /app

# === BUILD STAGE ===
FROM base AS build

RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy package files
COPY package.json yarn.lock ./

# Install all dependencies
RUN yarn install --frozen-lockfile

# Copy source code
COPY . .

# Build application
RUN yarn run build

# Reinstall only production dependencies
RUN yarn install --production --frozen-lockfile

# === RUNTIME STAGE ===
FROM base
ENV NODE_ENV=production

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

COPY --from=build /app /app
COPY --from=build /etc/ssl/certs /etc/ssl/certs

RUN chown -R appuser:appuser /app
USER appuser

CMD ["node", "agent.js", "start"]
```

**Key characteristics:**
- Uses `yarn install --production --frozen-lockfile` to reinstall only production deps (instead of pruning)
- Otherwise follows standard flow

### Yarn Berry (v2+)

[Yarn Berry Dockerfile](node.yarn-berry.Dockerfile) | [Yarn Berry Dockerignore](node.yarn-berry.dockerignore)

**Package files:** `package.json`, `yarn.lock`, `.yarnrc.yml`

```dockerfile
# === BASE STAGE ===
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base
WORKDIR /app

# Enable Corepack for Yarn Berry
RUN corepack enable

# === BUILD STAGE ===
FROM base AS build

RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy Yarn configuration
COPY .yarnrc.yml* ./

# Remove local yarnPath (use corepack version)
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Activate Yarn version
RUN corepack prepare yarn@stable --activate

# Copy package files
COPY package.json yarn.lock ./

# Install dependencies
RUN yarn install --immutable

# Copy source code
COPY . .

# Restore clean .yarnrc.yml
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Build application
RUN yarn run build

# Remove dev dependencies
RUN yarn workspaces focus --production

# === RUNTIME STAGE ===
FROM base
ENV NODE_ENV=production

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

COPY --from=build /app /app
COPY --from=build /etc/ssl/certs /etc/ssl/certs

RUN chown -R appuser:appuser /app
USER appuser

CMD ["node", "agent.js", "start"]
```

**Key characteristics:**
- Requires corepack enable
- May need to remove local yarnPath from .yarnrc.yml
- Uses `yarn workspaces focus --production` to remove dev dependencies

### Bun

[Bun Dockerfile](node.bun.Dockerfile) | [Bun Dockerignore](node.bun.dockerignore)

**Package files:** `package.json`, `bun.lockb`

```dockerfile
# === BASE STAGE (Different base image) ===
ARG BUN_VERSION=1
FROM oven/bun:${BUN_VERSION} AS base
WORKDIR /app

# === BUILD STAGE ===
FROM base AS build

# Copy package files
COPY package.json bun.lock* ./

# Install all dependencies
RUN bun install --frozen-lockfile

ENV NODE_ENV=production

# Copy source code
COPY . .

# Build if needed (Bun can run TypeScript directly)
RUN bun run build

# Reinstall production only
RUN bun install --production

# === RUNTIME STAGE ===
FROM base

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

COPY --from=build /app /app

RUN chown -R appuser:appuser /app
USER appuser

# Bun can run TypeScript directly
CMD ["bun", "run", "agent.ts", "start"]
```

**Key characteristics:**
- Uses oven/bun base image instead of node
- Can execute TypeScript directly without compilation
- No need for SSL certificate copying (included in base)

## Python Package Managers

### General Build Process

Python agents typically use a single-stage build (no compilation step like TypeScript):

**Build Process:**
- Set PYTHONUNBUFFERED=1 (ensures immediate log output)
- Create non-privileged user (appuser)
- Install system dependencies (gcc, python3-dev) if needed for native modules
- Set working directory to /app
- Copy dependency files (requirements.txt, pyproject.toml, etc.)
- Install Python dependencies (approach varies by package manager)
- Copy application code
- Change ownership to appuser
- Switch to appuser
- Pre-download models with `download-files` command
- Run application with CMD

### pip

[pip Dockerfile](python.pip.Dockerfile) | [pip Dockerignore](python.pip.dockerignore)

**Package files:** `requirements.txt`

```dockerfile
# Single-stage build (no compilation needed for pure Python)
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

# Create non-privileged user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install system dependencies if needed
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

# Change ownership
RUN chown -R appuser:appuser /app
USER appuser

# Pre-download models
RUN python agent.py download-files

CMD ["python", "agent.py", "start"]
```

**Key characteristics:**
- Simplest approach
- Use `--no-cache-dir` with pip to avoid storing cache in image

### UV

[UV Dockerfile](python.uv.Dockerfile) | [UV Dockerignore](python.uv.dockerignore)

**Package files:** `pyproject.toml`, `uv.lock`

```dockerfile
# UV uses its own optimized base image
ARG PYTHON_VERSION=3.11
FROM ghcr.io/astral-sh/uv:python${PYTHON_VERSION}-bookworm-slim

ENV PYTHONUNBUFFERED=1

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install system dependencies
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy dependency files with correct ownership
COPY --chown=appuser:appuser pyproject.toml uv.lock* ./

# Copy all application files
COPY --chown=appuser:appuser . .

# Ensure ownership
RUN chown -R appuser:appuser /app

# Switch to non-privileged user BEFORE installing
USER appuser

# Install dependencies with UV (creates venv as appuser)
RUN uv sync --locked --python $PYTHON_VERSION

# Pre-download models
RUN uv run agent.py download-files

CMD ["uv", "run", "agent.py", "start"]
```

**Key characteristics:**
- Uses different base image: `ghcr.io/astral-sh/uv:python${VERSION}-bookworm-slim`
- Must switch to appuser BEFORE `uv sync` (ownership issues in venv)
- UV creates and manages its own virtual environment
- 10-100x faster than pip

### Poetry

[Poetry Dockerfile](python.poetry.Dockerfile) | [Poetry Dockerignore](python.poetry.dockerignore)

**Package files:** `pyproject.toml`, `poetry.lock`

```dockerfile
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
# Disable virtualenv creation (container is already isolated)
ENV POETRY_HOME=/opt/poetry \
    POETRY_VIRTUALENVS_CREATE=false \
    POETRY_NO_INTERACTION=1 \
    POETRY_VERSION=1.8.3

ENV PATH="$POETRY_HOME/bin:$PATH"

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install Poetry and system dependencies
RUN apt-get update && \
    apt-get install -y \
    curl \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && curl -sSL https://install.python-poetry.org | python3 - --version $POETRY_VERSION

WORKDIR /app

# Copy dependency files
COPY pyproject.toml poetry.lock* ./

# Install dependencies without installing the project itself
RUN poetry install --no-root --no-dev

# Copy application code
COPY . .

# Install the project
RUN poetry install --only-root

RUN chown -R appuser:appuser /app
USER appuser

# Pre-download models
RUN python agent.py download-files

CMD ["python", "agent.py", "start"]
```

**Key characteristics:**
- Sets `POETRY_VIRTUALENVS_CREATE=false` (container is already isolated)
- Two-step install: dependencies first (`--no-root`), then project (`--only-root`)
- Installs Poetry via curl script

### Pipenv

[Pipenv Dockerfile](python.pipenv.Dockerfile) | [Pipenv Dockerignore](python.pipenv.dockerignore)

**Package files:** `Pipfile`, `Pipfile.lock`

```dockerfile
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
# Create .venv in project directory
ENV PIPENV_VENV_IN_PROJECT=1 \
    PIPENV_IGNORE_VIRTUALENVS=1

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

RUN chown -R appuser:appuser /app
USER appuser

# Set PATH to include virtual environment
ENV PATH="/app/.venv/bin:$PATH"

# Pre-download models
RUN python agent.py download-files

CMD ["python", "agent.py", "start"]
```

**Key characteristics:**
- Creates virtual environment in project directory (`PIPENV_VENV_IN_PROJECT=1`)
- Uses `--deploy` flag to ensure lock file consistency
- Must add `.venv/bin` to PATH after switching to appuser

### PDM

[PDM Dockerfile](python.pdm.Dockerfile) | [PDM Dockerignore](python.pdm.dockerignore)

**Package files:** `pyproject.toml`, `pdm.lock`

```dockerfile
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
# Don't use venv (use system Python)
ENV PDM_USE_VENV=false \
    PDM_IGNORE_SAVED_PYTHON=true

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

RUN chown -R appuser:appuser /app
USER appuser

# Pre-download models
RUN pdm run python agent.py download-files

CMD ["pdm", "run", "python", "agent.py", "start"]
```

**Key characteristics:**
- Sets `PDM_USE_VENV=false` to use system Python directly
- Two-step install: dependencies (`--no-self`), then project (`--no-editable`)
- Must use `pdm run` in CMD to ensure correct environment

### Hatch

[Hatch Dockerfile](python.hatch.Dockerfile) | [Hatch Dockerignore](python.hatch.dockerignore)

**Package files:** `pyproject.toml`, `hatch.toml`

```dockerfile
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim

ENV PYTHONUNBUFFERED=1
# Set virtual environment path
ENV HATCH_ENV_TYPE_VIRTUAL_PATH=/app/.venv

ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install Hatch and system dependencies
RUN apt-get update && \
    apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir hatch

WORKDIR /app

# Copy project files
COPY pyproject.toml ./
COPY hatch.toml* ./

# Create virtual environment
RUN hatch env create default

# Copy application code
COPY . .

# Install project in environment
RUN hatch run python -m pip install -e .

RUN chown -R appuser:appuser /app
USER appuser

# Set PATH to include virtual environment
ENV PATH="/app/.venv/bin:$PATH"

# Pre-download models
RUN hatch run python agent.py download-files

CMD ["hatch", "run", "python", "agent.py", "start"]
```

**Key characteristics:**
- Creates virtual environment at `/app/.venv` via `HATCH_ENV_TYPE_VIRTUAL_PATH`
- Uses `hatch env create default` to set up environment
- Installs project with `hatch run python -m pip install -e .`
- Must use `hatch run` in CMD

## Common Configurations

### Security Configuration

#### Non-Privileged User Setup

Containers should run with non-root privileges to limit potential damage from security vulnerabilities.

```dockerfile
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Install dependencies as root
RUN apt-get install... && pip install...

# Change ownership before switching user
RUN chown -R appuser:appuser /app

# Switch to non-privileged user for runtime
USER appuser
```

The UID 10001 is chosen to avoid conflicts with system users (1-999) while remaining in the standard user range.

### Environment Variables

#### Python Configuration

```dockerfile
ENV PYTHONUNBUFFERED=1
```

Disables output buffering to ensure logs are immediately available for debugging and monitoring.

#### Node.js Configuration

```dockerfile
ENV NODE_ENV=production
```

Enables production optimizations including reduced memory usage, optimized error handling, and exclusion of development dependencies.

#### SSL Certificates for Node.js

Node.js slim images require explicit certificate copying:

```dockerfile
COPY --from=build /etc/ssl/certs /etc/ssl/certs
```

This enables HTTPS requests to external services including LiveKit infrastructure.

### Native Module Dependencies

#### Node.js Native Modules

For packages with C++ addons (e.g., node-sass, bcrypt, sqlite3), add in the build stage:

```dockerfile
RUN apt-get update -qq && apt-get install --no-install-recommends -y \
    ca-certificates \
    python3 \          # node-gyp requires Python
    make \
    g++ \
    && rm -rf /var/lib/apt/lists/*
```

This must be installed BEFORE running `npm ci` or equivalent.

#### Python Native Modules

For packages with C extensions (e.g., numpy, pandas, psycopg2), add before pip install:

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*
```

### System Dependencies

#### Audio Processing

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc python3-dev \
    libasound2-dev \
    libportaudio2 \
    libsndfile1-dev \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*
```

##### PyAudio Setup

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc python3-dev \
    libasound2-dev \
    libportaudio2 \
    portaudio19-dev \
    && rm -rf /var/lib/apt/lists/*
```

##### Sounddevice Setup (Alternative to PyAudio)

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc python3-dev \
    libportaudio2 \
    && rm -rf /var/lib/apt/lists/*
```

Sounddevice offers simpler installation and improved error handling compared to PyAudio.

#### Computer Vision

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc python3-dev \
    libopencv-dev \
    libjpeg-dev \
    libpng-dev \
    libwebp-dev \
    && rm -rf /var/lib/apt/lists/*
```

#### Machine Learning

```dockerfile
RUN apt-get update && apt-get install -y \
    gcc g++ gfortran \
    python3-dev \
    libblas-dev \
    liblapack-dev \
    libatlas-base-dev \
    && rm -rf /var/lib/apt/lists/*
```

## Build Optimization

### Layer Caching Strategy

Docker caches each layer. Order instructions to maximize cache reuse:

```dockerfile
# Good: Dependencies cached separately from code
COPY package*.json ./       # Layer 1: Rarely changes
RUN npm ci                  # Layer 2: Expensive, cached
COPY . .                    # Layer 3: Frequently changes
RUN npm run build          # Layer 4: Depends on code

# Bad: Any code change invalidates all layers
COPY . .                    # Everything copied first
RUN npm ci && npm run build # Must rebuild everything
```

### Image Size Reduction

Reducing the image size helps speed up agent deployment.

```dockerfile
# Clean package manager caches
RUN pip install --no-cache-dir -r requirements.txt  # Saves 100-500MB
RUN npm ci && npm cache clean --force              # Saves 50-200MB

# Clean apt lists after installation
RUN apt-get update && apt-get install -y packages \
    && rm -rf /var/lib/apt/lists/*                 # Saves 20-50MB

# Remove Python bytecode cache
RUN find /app -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true

# Remove test files if not needed
RUN find /app -type d -name tests -exec rm -rf {} + 2>/dev/null || true
```

## Runtime Configuration

### Health Check Configuration

LiveKit agents automatically expose port 8081 for health monitoring. This endpoint is managed by the LiveKit SDK and should not be used for custom endpoints. The health check responds to HTTP GET requests at `/health` and returns 200 OK when the agent is connected to LiveKit.

## Troubleshooting

### Lock File Generation

Generate lock files locally before building:

```bash
# Node.js
npm install      # generates package-lock.json
pnpm install     # generates pnpm-lock.yaml
yarn install     # generates yarn.lock

# Python
poetry lock      # generates poetry.lock
uv lock          # generates uv.lock
pdm lock         # generates pdm.lock
pipenv lock      # generates Pipfile.lock
```

### Permission Issues

Ensure correct ownership transition:

```dockerfile
# Install as root
RUN pip install -r requirements.txt

# Change ownership before user switch
RUN chown -R appuser:appuser /app

# Switch to non-privileged user
USER appuser
```

### Native Module Build Failures

Install compilation tools before package installation:

```dockerfile
# Install system dependencies first
RUN apt-get update && apt-get install -y \
    gcc python3-dev build-essential \
    && rm -rf /var/lib/apt/lists/*

# Then install packages
RUN pip install -r requirements.txt
```

### Package Manager-Specific Considerations

#### Yarn Berry Plug'n'Play

For packages incompatible with PnP:

```yaml
# .yarnrc.yml
nodeLinker: node-modules
```

#### pnpm Symlinks

If Docker doesn't preserve symlinks correctly:

```dockerfile
RUN pnpm install --shamefully-hoist
```

#### Poetry Virtual Environments

Disable virtual environment creation in containers:

```dockerfile
ENV POETRY_VIRTUALENVS_CREATE=false
```

#### UV Python Version

Ensure Docker Python version matches project requirements:

```dockerfile
ARG PYTHON_VERSION=3.11
FROM python:${PYTHON_VERSION}-slim
```

## References

- [LiveKit Agents Documentation](https://docs.livekit.io/agents/)
- [LiveKit Agent Templates](https://github.com/livekit/livekit-cli/tree/main/pkg/agentfs/examples)
- [LiveKit Cloud Deployment](https://docs.livekit.io/agents/ops/deployment/cloud/build)
- [Docker Best Practices](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/)