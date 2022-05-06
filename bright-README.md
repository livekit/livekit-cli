# Bright load tester
Forked from livekit/livekit-cli
Should be able to keep in sync by merging upstream every once in awhile, changes are minimal, so shouldn't be too many merge conflicts.

## Create new Docker image
We use the docker image in the cloud build Test-Users-Run-Livekit-Simulcast job.
There is a makefile, simply run:
```
make -f bright-make.mk
```
or you can run with the following

```
docker build -t gcr.io/bright-live-staging/bright-with-livekit-test-user -f Dockerfile .
docker push gcr.io/bright-live-staging/bright-with-livekit-test-user
```