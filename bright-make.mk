REPO=us.gcr.io/bright-live-staging/bright-with-livekit-test-user
TAG=latest

all: build publish

build:
	docker build -f Dockerfile -t $(REPO):$(TAG) .

publish:
	docker push $(REPO):$(TAG)

clean:
	docker images --no-trunc --format '{{.ID}} {{.Repository}}' | grep $(REPO) | awk '{ print $1 }' | xargs -t docker rmi