# This is an example Dockerfile that builds a minimal container for running LK Agents
# syntax=docker/dockerfile:1
FROM node:20-slim AS base

WORKDIR /app

RUN npm install -g pnpm@9.7.0

# Throw away build stage to reduce size of final image
FROM base AS build

RUN apt-get update -qq && apt-get install --no-install-recommends -y ca-certificates
COPY --link . .

RUN pnpm install --frozen-lockfile
RUN pnpm run build

FROM base
COPY --from=build /app /app
COPY --from=build /etc/ssl/certs /etc/ssl/certs

# Start the server by default, this can be overwritten at runtime
EXPOSE 8081

CMD [ "node", "./dist/agent.js", "start" ]

