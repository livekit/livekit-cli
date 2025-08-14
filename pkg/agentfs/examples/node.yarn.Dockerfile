# This Dockerfile creates a production-ready container for a LiveKit Node.js agent using Yarn Classic (v1)
# It uses a multi-stage build to minimize the final image size
# syntax=docker/dockerfile:1

# === MULTI-STAGE BUILD STRUCTURE ===
# Stage 1 (base): Sets up Node.js environment
# Stage 2 (build): Installs dependencies and builds the application
# Stage 3 (final): Copies only necessary files for runtime
#
# Benefits: Smaller final image without build tools and source files
# Final image contains only: compiled JS, node_modules, and runtime dependencies

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base

# Define the program entrypoint file where your agent is started.
ARG PROGRAM_MAIN="{{.ProgramMain}}"

# Set the working directory where our application will live
WORKDIR /app

# === BUILD STAGE ===
# This stage is discarded after building, keeping the final image small
FROM base AS build

# Install CA certificates for HTTPS connections during package installation
# --no-install-recommends keeps the image smaller by avoiding suggested packages
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy package.json and yarn.lock first for better layer caching
# This allows Docker to cache the dependency installation step
COPY package.json yarn.lock ./

# Install dependencies using yarn
# --frozen-lockfile ensures exact versions from yarn.lock are used
# Install all dependencies including dev for the build stage
# This provides reproducible builds across different environments
RUN yarn install --frozen-lockfile

# Copy all application files into the build container
COPY . .

# Build the TypeScript application
# This compiles TypeScript to JavaScript and prepares for production
RUN yarn run build

# Remove any non-production dependencies that might have been needed for build
# This reduces the final image size
RUN yarn install --production --frozen-lockfile

# === FINAL PRODUCTION STAGE ===
# Start from the base image without build tools
FROM base

# Set production environment for runtime
ENV NODE_ENV=production

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

# Copy the built application from the build stage
# This includes node_modules and compiled JavaScript files
COPY --from=build /app /app

# Copy SSL certificates for HTTPS connections at runtime
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /app

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Run the application
# The "start" command tells the agent to connect to LiveKit and begin waiting for jobs
CMD [ "node", "{{.ProgramMain}}", "start" ]

# === COMMON CUSTOMIZATIONS ===
#
# 1. Production-only dependencies:
#    To install only production dependencies (exclude devDependencies):
#    RUN yarn install --frozen-lockfile --production
#
# 2. Installing system dependencies for native modules:
#    Some Node.js packages require system libraries. Add before COPY in build stage:
#
#    # For packages with native C++ addons:
#    RUN apt-get update -qq && apt-get install --no-install-recommends -y \
#        ca-certificates \
#        python3 \
#        make \
#        g++ \
#        && rm -rf /var/lib/apt/lists/*
#
# 3. Different entry point locations:
#    - If using src/index.js: CMD ["node", "./src/index.js", "start"]
#    - If using dist/main.js: CMD ["node", "./dist/main.js", "start"]
#    - For development: CMD ["yarn", "run", "dev"]
#
# 4. Environment variables:
#    Set Node.js environment for production:
#    ENV NODE_ENV=production
#
# 5. Running as non-root user (recommended for security):
#    Add before the final CMD:
#    RUN adduser --disabled-password --gecos "" --uid 10001 appuser
#    USER appuser
#
# === TROUBLESHOOTING YARN-SPECIFIC ISSUES ===
#
# 1. "yarn.lock not found":
#    - Run `yarn install` locally to generate yarn.lock
#    - Commit yarn.lock to version control
#    - --frozen-lockfile requires lock file for reproducible builds
#
# 2. "Module not found" errors:
#    - Ensure all dependencies are in package.json
#    - Check that build output is in the expected location
#    - Verify node_modules are copied correctly
#    - Check for hoisting issues with yarn workspaces
#
# 3. "EACCES: permission denied" errors:
#    - Add a non-root user (see example above)
#    - Ensure files have correct permissions
#    - Clear yarn cache if needed: yarn cache clean
#
# 4. Large image sizes:
#    - Use node:20-alpine instead of node:20-slim for smaller base
#    - Ensure .dockerignore excludes unnecessary files
#    - Use yarn install --production in final stage
#    - Consider using yarn autoclean to remove unnecessary files
#
# 5. Slow builds:
#    - Use Docker BuildKit: DOCKER_BUILDKIT=1 docker build
#    - Order COPY commands from least to most frequently changed
#    - Copy package.json and yarn.lock before source code for better caching
#    - Consider using yarn install --prefer-offline for faster installs
#
# 6. Native module compilation issues:
#    - Install build tools in the build stage (see customization #2)
#    - For node-gyp: apt-get install python3 make g++
#    - Consider using prebuilt binaries when available
#
# 7. Runtime connection issues:
#    - Verify the agent can reach the LiveKit server
#    - Check that required environment variables are set
#    - Ensure the healthcheck endpoint (8081) is accessible
#
# 8. Yarn workspace issues:
#    - If using workspaces, ensure all workspace packages are copied
#    - Use --frozen-lockfile --ignore-scripts for security
#    - Consider nohoist configuration for problematic packages
#
# 9. Network timeout issues:
#    - Increase network timeout: yarn install --network-timeout 100000
#    - Use a yarn registry mirror if behind corporate proxy
#    - Configure yarn proxy settings if needed
#
# For more help: https://docs.livekit.io/agents/
# For build options and troubleshooting: https://docs.livekit.io/agents/ops/deployment/cloud/build