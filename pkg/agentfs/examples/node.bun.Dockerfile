# This Dockerfile creates a production-ready container for a LiveKit Node.js agent using Bun
# It uses a multi-stage build to minimize the final image size
# syntax=docker/dockerfile:1

# === MULTI-STAGE BUILD STRUCTURE ===
# Stage 1 (base): Sets up Bun runtime environment
# Stage 2 (build): Installs dependencies and builds the application
# Stage 3 (final): Copies only necessary files for runtime
#
# Benefits: Smaller final image without build tools and source files
# Final image contains only: compiled JS, node_modules, and runtime dependencies

# Use official Bun image as base
ARG BUN_VERSION=1
FROM oven/bun:${BUN_VERSION} AS base

# Define the program entrypoint file where your agent is started.
ARG PROGRAM_MAIN="{{.ProgramMain}}"

# Set the working directory where our application will live
WORKDIR /app

# === BUILD STAGE ===
# This stage is discarded after building, keeping the final image small
FROM base AS build

# Copy package.json and lock file first for better layer caching
# This allows Docker to cache the dependency installation step
COPY package.json bun.lock* ./

# Install dependencies using bun
# Bun automatically uses the lock file if it exists
# Install all dependencies including dev for the build stage
RUN bun install --frozen-lockfile

# Set production environment
ENV NODE_ENV=production

# Copy all application files into the build container
COPY . .

# Build the TypeScript application (if needed)
# Bun can run TypeScript directly, but building may still be needed for bundling
RUN bun run build

# Prune any dev dependencies that might have been needed for build
# This keeps only production dependencies
RUN bun install --production

# === FINAL PRODUCTION STAGE ===
# Start from the base image without build tools
FROM base

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

# Change ownership of all app files to the non-privileged user
# This ensures the application can read/write files as needed
RUN chown -R appuser:appuser /app

# Switch to the non-privileged user for all subsequent operations
# This improves security by not running as root
USER appuser

# Run the application using Bun
# The "start" command tells the agent to connect to LiveKit and begin waiting for jobs
# Bun can run TypeScript directly, but we use the built JS for production
CMD [ "bun", "run", "{{.ProgramMain}}", "start" ]

# === COMMON CUSTOMIZATIONS ===
#
# 1. Production-only dependencies:
#    To install only production dependencies (exclude devDependencies):
#    RUN bun install --frozen-lockfile --production
#
# 2. Direct TypeScript execution:
#    Bun can run TypeScript files directly without compilation:
#    CMD ["bun", "run", "./src/agent.ts", "start"]
#
# 3. Different entry point locations:
#    - If using src/index.ts: CMD ["bun", "run", "./src/index.ts", "start"]
#    - If using dist/main.js: CMD ["bun", "run", "./dist/main.js", "start"]
#    - For development: CMD ["bun", "run", "dev"]
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
# 6. Using Node.js compatibility mode:
#    Some packages may need Node.js APIs:
#    CMD ["bun", "--bun", "run", "{{.ProgramMain}}", "start"]
#
# === TROUBLESHOOTING BUN-SPECIFIC ISSUES ===
#
# 1. "bun.lock not found":
#    - Run `bun install` locally to generate bun.lock
#    - Commit bun.lock to version control for reproducible builds
#    - Use --frozen-lockfile for production builds
#
# 2. "Module not found" errors:
#    - Ensure all dependencies are in package.json
#    - Bun uses a different module resolution than Node.js
#    - Try using --bun flag for better compatibility
#    - Check that node_modules are copied correctly
#
# 3. Node.js API compatibility:
#    - Some Node.js APIs may not be fully implemented
#    - Use --bun flag to enable Bun's runtime
#    - Consider fallback to Node.js for incompatible packages
#
# 4. TypeScript issues:
#    - Bun runs TypeScript natively, no compilation needed
#    - However, some TypeScript features may differ
#    - Check tsconfig.json compatibility with Bun
#
# 5. Large image sizes:
#    - Use oven/bun:alpine for smaller base image
#    - Ensure .dockerignore excludes unnecessary files
#    - Use bun install --production for production builds
#
# 6. Performance differences:
#    - Bun is generally faster for startup and execution
#    - However, some optimizations may differ from Node.js
#    - Profile your application if performance issues arise
#
# 7. Native module issues:
#    - Bun has different native module support than Node.js
#    - Some Node.js native modules may not work
#    - Check Bun's compatibility list for your dependencies
#
# 8. Runtime connection issues:
#    - Verify the agent can reach the LiveKit server
#    - Check that required environment variables are set
#    - Ensure the healthcheck endpoint (8081) is accessible
#
# For more help: https://bun.sh/docs
# For LiveKit agent build help: https://docs.livekit.io/agents/ops/deployment/cloud/build