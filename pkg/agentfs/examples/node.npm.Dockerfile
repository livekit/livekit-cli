# This Dockerfile creates a production-ready container for a LiveKit Node.js agent using npm
# It uses a multi-stage build to minimize the final image size
# syntax=docker/dockerfile:1

# === MULTI-STAGE BUILD STRUCTURE ===
# Stage 1 (base): Sets up Node.js environment
# Stage 2 (build): Installs dependencies and builds the application
# Stage 3 (final): Copies only necessary files for runtime
#
# Benefits: Smaller final image without build tools and source files
# Final image contains only: compiled JS, node_modules, and runtime dependencies

FROM node:20-slim AS base

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

# Copy package.json and package-lock.json first for better layer caching
# This allows Docker to cache the dependency installation step
COPY package*.json ./

# Install dependencies using npm ci
# npm ci is faster and more reliable for production builds than npm install
# It requires package-lock.json and installs exact versions
RUN npm ci

# Copy all application files into the build container
COPY . .

# Build the TypeScript application
# This compiles TypeScript to JavaScript and prepares for production
RUN npm run build

# === FINAL PRODUCTION STAGE ===
# Start from the base image without build tools
FROM base

# Copy the built application from the build stage
# This includes node_modules and compiled JavaScript files
COPY --from=build /app /app

# Copy SSL certificates for HTTPS connections at runtime
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Expose the healthcheck port
# This allows Docker and orchestration systems to check if the container is healthy
EXPOSE 8081

# Run the application
# The "start" command tells the agent to connect to LiveKit and begin waiting for jobs
CMD [ "node", "{{.ProgramMain}}", "start" ]

# === COMMON CUSTOMIZATIONS ===
#
# 1. Production-only dependencies:
#    To install only production dependencies (exclude devDependencies):
#    RUN npm ci --omit=dev
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
#    - For development: CMD ["npm", "run", "dev"]
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
# === TROUBLESHOOTING NPM-SPECIFIC ISSUES ===
#
# 1. "package-lock.json not found":
#    - Run `npm install` locally to generate package-lock.json
#    - Commit package-lock.json to version control
#    - npm ci requires package-lock.json for reproducible builds
#
# 2. "Module not found" errors:
#    - Ensure all dependencies are in package.json
#    - Check that build output is in the expected location
#    - Verify node_modules are copied correctly
#
# 3. "EACCES: permission denied" errors:
#    - Add a non-root user (see example above)
#    - Ensure files have correct permissions
#
# 4. Large image sizes:
#    - Use node:20-alpine instead of node:20-slim for smaller base
#    - Ensure .dockerignore excludes unnecessary files
#    - Consider using npm prune --production after build
#    - Use npm ci --omit=dev for production installs
#
# 5. Slow builds:
#    - Use Docker BuildKit: DOCKER_BUILDKIT=1 docker build
#    - Order COPY commands from least to most frequently changed
#    - Copy package.json and package-lock.json before source code for better caching
#    - npm ci is faster than npm install for CI/production builds
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
# For more help: https://docs.livekit.io/agents/