# This is an example Dockerfile that builds a minimal container for running LK Agents
# For more information on the build process, see https://docs.livekit.io/agents/ops/deployment/builds/
# syntax=docker/dockerfile:1

# Use the official Node.js v22 base image with Node.js 22.10.0
# We use the slim variant to keep the image size smaller while still having essential tools
ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base

# Configure pnpm installation directory and ensure it is on PATH
ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"

# Install required system packages and pnpm, then clean up the apt cache for a smaller image
# ca-certificates: enables TLS/SSL for securely fetching dependencies and calling HTTPS services
# --no-install-recommends keeps the image minimal
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates && rm -rf /var/lib/apt/lists/*

# Pin pnpm version for reproducible builds
RUN npm install -g pnpm@10

# Create a new directory for our application code
# And set it as the working directory
WORKDIR /app

# Copy just the dependency files first, for more efficient layer caching
COPY package.json pnpm-lock.yaml ./

# Install dependencies using pnpm
# --frozen-lockfile ensures we use exact versions from pnpm-lock.yaml for reproducible builds
RUN pnpm install --frozen-lockfile

# Copy all remaining pplication files into the container
# This includes source code, configuration files, and dependency specifications
# (Excludes files specified in .dockerignore)
COPY . .

# Build the project
# Your package.json must contain a "build" script, such as `"build": "tsc"`
RUN pnpm build

# Create a non-privileged user that the app will run under
# See https://docs.docker.com/develop/develop-images/dockerfile_best_practices/#user
ARG UID=10001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/app" \
    --shell "/sbin/nologin" \
    --uid "${UID}" \
    appuser

# Set proper permissions
RUN chown -R appuser:appuser /app
USER appuser

# Pre-download any ML models or files the agent needs
# This ensures the container is ready to run immediately without downloading
# dependencies at runtime, which improves startup time and reliability
# Your package.json must contain a "download-files" script, such as `"download-files": "pnpm run build && node dist/agent.js download-files"`
RUN pnpm download-files

# Switch back to root to remove dev dependencies and finalize setup
USER root
RUN pnpm prune --prod && chown -R appuser:appuser /app
USER appuser

# Set Node.js to production mode
ENV NODE_ENV=production

# Run the application
# The "start" command tells the worker to connect to LiveKit and begin waiting for jobs.
# Your package.json must contain a "start" script, such as `"start": "node dist/agent.js start"`
CMD [ "pnpm", "start" ]