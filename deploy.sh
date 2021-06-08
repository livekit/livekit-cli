#!/bin/bash

set -euxo pipefail

docker build -t load-tester .
IMG=$(docker images -q | awk 'NR==1')
docker tag load-tester:latest 203125320322.dkr.ecr.us-west-2.amazonaws.com/lk-load-tester:"$IMG"
docker push 203125320322.dkr.ecr.us-west-2.amazonaws.com/lk-load-tester:"$IMG"

# kon app deploy --tag "$IMG" load-tester
