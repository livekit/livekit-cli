# This Dockerfile creates a production-ready container for a LiveKit Node.js agent using Yarn Berry (v2+)
# It uses a multi-stage build to minimize the final image size
# syntax=docker/dockerfile:1

# === MULTI-STAGE BUILD STRUCTURE ===
# Stage 1 (base): Sets up Node.js environment with Yarn Berry
# Stage 2 (build): Installs dependencies and builds the application
# Stage 3 (final): Copies only necessary files for runtime
#
# Benefits: Smaller final image without build tools and source files
# Final image contains only: compiled JS, node_modules (or PnP), and runtime dependencies

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-slim AS base

# Define the program entrypoint file where your agent is started.
ARG PROGRAM_MAIN="{{.ProgramMain}}"

# Set the working directory where our application will live
WORKDIR /app

# Enable Corepack to use Yarn Berry
# Corepack is Node.js's official package manager manager
RUN corepack enable

# === BUILD STAGE ===
# This stage is discarded after building, keeping the final image small
FROM base AS build

# Install CA certificates for HTTPS connections during package installation
# --no-install-recommends keeps the image smaller by avoiding suggested packages
RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates

# Copy Yarn Berry configuration if it exists
# We'll override the yarnPath to not rely on local releases
COPY .yarnrc.yml* ./

# Override yarnPath in .yarnrc.yml to use corepack-managed yarn
# Keep other settings like nodeLinker
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Set up Yarn Berry version using corepack
RUN corepack prepare yarn@stable --activate

# Copy package.json and yarn.lock for better layer caching
COPY package.json yarn.lock ./

# Install dependencies using Yarn Berry
# --immutable ensures exact versions from yarn.lock are used
# --immutable-cache ensures the cache is not modified
RUN yarn install --immutable

# Copy all application files into the build container
# But preserve our modified .yarnrc.yml
COPY . .
RUN if [ -f .yarnrc.yml ]; then \
      grep -v "yarnPath:" .yarnrc.yml > .yarnrc.yml.tmp && mv .yarnrc.yml.tmp .yarnrc.yml; \
    fi

# Build the TypeScript application
# This compiles TypeScript to JavaScript and prepares for production
RUN yarn run build

# Install production dependencies only for final image
# This removes dev dependencies after build
RUN yarn workspaces focus --production

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
# This includes dependencies and compiled JavaScript files
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
# 1. Zero-Install (PnP - Plug'n'Play) mode:
#    If using Yarn Berry's PnP mode, ensure .pnp.cjs is copied:
#    COPY --from=build /app/.pnp.* ./
#    And update CMD to use yarn node:
#    CMD ["yarn", "node", "{{.ProgramMain}}", "start"]
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
# === TROUBLESHOOTING YARN BERRY-SPECIFIC ISSUES ===
#
# 1. "yarn.lock not found":
#    - Run `yarn install` locally to generate yarn.lock
#    - Commit yarn.lock to version control
#    - --immutable requires lock file for reproducible builds
#
# 2. ".yarnrc.yml not found":
#    - Ensure .yarnrc.yml is committed to version control
#    - This file contains Yarn Berry configuration
#    - Run `yarn set version berry` locally to migrate from Yarn Classic
#
# 3. PnP (Plug'n'Play) issues:
#    - If using PnP, ensure .pnp.cjs and .pnp.loader.mjs are copied
#    - Use `yarn node` instead of `node` to run with PnP
#    - Some packages may need to be unplugged: yarn unplug <package>
#
# 4. "Module not found" errors:
#    - Check if using PnP or node_modules (nodeLinker setting)
#    - For PnP: ensure .pnp.* files are copied
#    - For node_modules: ensure they're copied correctly
#    - Check for hoisting issues with nmMode setting
#
# 5. Zero-Install not working:
#    - Ensure .yarn/cache is committed (for Zero-Install)
#    - Or add .yarn/cache to .gitignore (for traditional install)
#    - Check compressionLevel setting in .yarnrc.yml
#
# 6. Native module compilation issues:
#    - Install build tools in the build stage (see customization #2)
#    - For node-gyp: apt-get install python3 make g++
#    - Consider supportedArchitectures in .yarnrc.yml
#
# 7. Large image sizes:
#    - Use node:20-alpine instead of node:20-slim for smaller base
#    - If using Zero-Install, consider excluding .yarn/cache from Docker
#    - Use nmMode: hardlinks-local for smaller node_modules
#
# 8. Workspace issues:
#    - Ensure all workspace packages are included
#    - Use `yarn workspaces focus` for single workspace deployment
#    - Check nmHoistingLimits setting for workspace hoisting
#
# 9. Plugin issues:
#    - Ensure .yarn/plugins directory is copied if using plugins
#    - Plugins must be compatible with production environment
#    - Consider disabling unnecessary plugins for production
#
# For more help: https://yarnpkg.com/migration/guide
# For LiveKit agent build help: https://docs.livekit.io/agents/ops/deployment/cloud/build