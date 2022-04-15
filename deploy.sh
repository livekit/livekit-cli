#!/bin/bash

set -euxo pipefail

docker buildx build --platform linux/amd64 -t livekit/load-tester:latest .
IMG=$(docker images -q | awk 'NR==1')
docker tag livekit/load-tester:latest livekit/load-tester:"$IMG"
docker push livekit/load-tester:"$IMG"
docker push livekit/load-tester:latest
