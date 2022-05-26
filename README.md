# LiveKit CLI & Load Tester

This package includes command line utilities that interacts with LiveKit. It allows you to:

-   Create access tokens
-   Access LiveKit APIs, create, delete rooms, etc
-   Join a room as a participant, inspecting in-room events
-   Perform load testing, efficiently simulating real-world load

## Installation

This repo uses Git LFS for embedded video resources. Please ensure git-lfs is installed on your machine.

```shell
$ go install github.com/livekit/livekit-cli/cmd/livekit-cli@latest
$ go install github.com/livekit/livekit-cli/cmd/livekit-load-tester@latest
```

## Usage

## livekit-cli

```shell
% ./bin/livekit-cli --help
NAME:
   livekit-cli - CLI client to LiveKit

USAGE:
   livekit-cli [global options] command [command options] [arguments...]

VERSION:
   0.7.2

COMMANDS:
   help, h  Shows a list of commands or help for one command
   Egress:
     start-room-composite-egress   Start room composite egress
     start-track-composite-egress  Start track composite egress
     start-track-egress            Start track egress
     list-egress                   List all active egress
     update-layout                 Updates layout for a live room composite egress
     update-stream                 Adds or removes rtmp output urls from a live stream
     stop-egress                   Stop egress
     test-egress-template          See what your egress template will look like in a recording
   Participant:
     join-room  joins a room as a participant
   Recording:
     start-recording  Starts a recording with a deployed recorder service
     add-output       Adds an rtmp output url to a live recording
     remove-output    Removes an rtmp output url from a live recording
     end-recording    Ends a recording
   RoomService:
     create-room
     list-rooms
     delete-room
     update-room-metadata
     list-participants
     get-participant
     remove-participant
     update-participant
     mute-track
     update-subscriptions
   Token:
     create-token  creates an access token

GLOBAL OPTIONS:
   --verbose      (default: false)
   --help, -h     show help (default: false)
   --version, -v  print the version (default: false)
```

### Publishing to a room

#### Demo video track

To publish a demo video as a participant's track, use the following.

```shell
% ./bin/livekit-cli join-room --room yourroom --identity publisher \
  --publish-demo
```

It'll publish the video track with Simulcast, at 720p, 360p, and 180p.

#### Publish files as tracks

You can publish your own audio/video files. These tracks files need to be encoded in supported codecs.
Refer to [encoding instructions](https://github.com/livekit/server-sdk-go/tree/main#publishing-tracks-to-room)

```shell
% ./bin/livekit-cli join-room --room yourroom --identity publisher \
  --publish-file path/to/video.ivf \
  --publish-file path/to/audio.ogg \
  --fps 23.98
```

This will publish the pre-encoded ivf and ogg files to the room, indicating video FPS of 23.98.

#### Publish stream via stdin

You can pipe a single stream of video / audio data from another process to the CLI. The following example uses `cat`, but instead of publishing as a file, we're using Unix pipe.

```shell
% cat /path/to/video.h264 | ./bin/livekit-cli join-room --room yourroom --identity
publisher \
  --publish-stdin {one of video/h264, video/vp8, audio/opus} \
  --fps 23.98
```

#### Publish stream via Unix socket

If you need to handle multiple streams, you can use Unix socket. In this mode, the CLI acts as socket listener. The socket name must contain one of the keywords (`opus`, `vp8` or `h264`) so the CLI can infer which codec reader to use.

The following example uses `cat` and `netcat (nc)` to publish video & audio tracks. For use in your application, you can look up how to send data to Unix domain socket in your language (yes, any language!).

Video:

```shell
% cat /path/to/video.h264 | nc -l -U /tmp/livekit-h264.sock
```

Audio:

```shell
% cat /path/to/audio.ogg | nc -l -U /tmp/livekit-opus.sock
```

LiveKit CLI:

```shell
./bin/livekit-cli join-room --room yourroom --identity
publisher \
  --publish-socket /tmp/livekit-opus.sock \
  --publish-socket /tmp/livekit-h264.sock \
  --fps 23.98
```

Note that with `netcat`, you need to remove the socket files when you want to reuse it, otherwise you'll get `bind: address already in use` error:

```shell
% rm /tmp/livekit-opus.sock /tmp/livekit-h264.sock
```

### Recording & egress

Recording requires [egress service](https://docs.livekit.io/guides/egress/) to be set up first.

Example request.json files are [located here](https://github.com/livekit/livekit-cli/tree/main/cmd/livekit-cli/examples).

```shell
# start room composite (recording of room UI)
livekit-cli start-room-composite-egress --url <your-url> --api-key <key> --api-secret <secret> --request request.json

# start track composite (audio + video)
livekit-cli start-track-composite-egress --url <your-url> --api-key <key> --api-secret <secret> --request request.json

# start track egress (single audio or video track)
livekit-cli start-track-egress --url <your-url> --api-key <key> --api-secret <secret> --request request.json
```

## livekit-load-tester

Load testing utility for LiveKit. This tool is quite versatile and is able to simulate various types of load.

### Quickstart

This guide requires a LiveKit server instance to be set up. You can start a load tester with:

```shell
$ ./livekit-load-tester --url <your-url> \
    --api-key <key> --api-secret <secret> \
    --room test-room --publishers 24
```

This simulates 8 video publishers to the room, with no subscribers. Video tracks are published with simulcast, at 720p, 360p, and 180p.

#### Watch the test

Use `livekit-cli` to generate a token so you can log into the room:

```shell
$ ./livekit-cli create-token --join --api-key <key> --api-secret <secret> \
    --room test-room --identity user
```

Head over to the [example web client](https://example.livekit.io) and paste in the token, you can see the simulated tracks published by the load tester.

![Load tester screenshot](misc/load-test-screenshot.jpg?raw=true)

### Running on a cloud VM

Due to bandwidth limitations of your ISP, most of us wouldn't have sufficient bandwidth to be able to simulate 100s of users download/uploading from the internet.

We recommend running the load tester from a VM on a cloud instance, where there isn't a bandwidth constraint.

To make this simple, `make` will generate a linux amd64 binary in `bin/`. You can scp the binary to a server instance and run the test there.

### Configuring system settings

Prior to running the load tests, it's important to ensure file descriptor limits have been set correctly. See [Performance tuning docs](https://docs.livekit.io/deploy/test-monitor#performance-tuning).

On the machine that you are running the load tester, they would also need to be applied:

```shell
ulimit -n 65535
sysctl -w fs.file-max=2097152
sysctl -w net.core.somaxconn=65535
sysctl -w net.core.rmem_max=25165824
sysctl -w net.core.wmem_max=25165824
```

### Simulate subscribers

You can run the load tester on multiple machines, each simulating any number of publishers or subscribers.

LiveKit SFU's performance is [measured by](https://docs.livekit.io/deploy/benchmark#measuring-performance) the amount
of data sent to its subscribers.

Use this command to simulate a load test of 5 publishers, and 500 subscribers:

```shell
$ ./livekit-load-tester --url <your-url> \
  --api-key <key> \
  --api-secret <secret> \
  --duration 1m \
  --publishers 5 \
  --subscribers 500
```

It'll print a report like the following. (this run was performed on a 16 core, 32GB memory VM)

```
Summary | Tester  | Tracks    | Bitrate                 | Latency     | Total Dropped
        | Sub 0   | 10/10     | 2.2mbps                 | 78.86829ms  | 0 (0%)
        | Sub 1   | 10/10     | 2.2mbps                 | 78.796542ms | 0 (0%)
        | Sub 10  | 10/10     | 2.2mbps                 | 79.361718ms | 0 (0%)
        | Sub 100 | 10/10     | 2.2mbps                 | 79.449831ms | 0 (0%)
        | Sub 101 | 10/10     | 2.2mbps                 | 80.001104ms | 0 (0%)
        | Sub 102 | 10/10     | 2.2mbps                 | 79.833373ms | 0 (0%)
...
        | Sub 97  | 10/10     | 2.2mbps                 | 79.374331ms | 0 (0%)
        | Sub 98  | 10/10     | 2.2mbps                 | 79.418816ms | 0 (0%)
        | Sub 99  | 10/10     | 2.2mbps                 | 79.768568ms | 0 (0%)
        | Total   | 5000/5000 | 678.7mbps (1.4mbps avg) | 79.923769ms | 0 (0%)
```

### Advanced usage

You could customize various parameters of the test such as

-   --publishers: number of publishers
-   --subscribers: number of publishers
-   --audio-bitrate: publishing audio bitrate; 0 to disable
-   --video-resolution: publishing video resolution. low, medium, high; none to disable
-   --no-simulcast: disables simulcast
-   --num-per-second: number of testers to start each second
-   --layout: layout to simulate (speaker, 3x3, 4x4, or 5x5)
