# LiveKit CLI & Load Tester

This package includes command line utilities that interacts with LiveKit. It allows you to:

-   Create access tokens
-   Access LiveKit APIs, create, delete rooms, etc
-   Join a room as a participant, inspecting in-room events
-   Perform load testing, efficiently simulating real-world load

## Installation

You can download [latest release here](https://github.com/livekit/livekit-cli/releases/latest). 

### Building from source

This repo uses [Git LFS](https://git-lfs.github.com/) for embedded video resources. Please ensure git-lfs is installed on your machine.

```shell
git clone github.com/livekit/livekit-cli
make install
```

## Usage

## livekit-cli

```shell
% livekit-cli --help
NAME:
   livekit-cli - CLI client to LiveKit

USAGE:
   livekit-cli [global options] command [command options] [arguments...]

VERSION:
   0.8.1

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
% livekit-cli join-room --room yourroom --identity publisher \
  --publish-demo
```

It'll publish the video track with Simulcast, at 720p, 360p, and 180p.

#### Publish files

You can publish your own audio/video files. These tracks files need to be encoded in supported codecs.
Refer to [encoding instructions](https://github.com/livekit/server-sdk-go/tree/main#publishing-tracks-to-room)

```shell
% livekit-cli join-room --room yourroom --identity publisher \
  --publish path/to/video.ivf \
  --publish path/to/audio.ogg \
  --fps 23.98
```

This will publish the pre-encoded ivf and ogg files to the room, indicating video FPS of 23.98. Note that the FPS only affects the video; audio is saved using preset bitrate.

#### Publish from Unix sockets

If you need to handle live media in your application, you can use Unix Domain Socket (UDS), which is a shared memory in a Unix-based OS. In this mode, the CLI listens to the socket and pushes incoming data to the room.

The argument passed should be in the format `--publish unix:{socket-name}`. The socket name must contain one of the keywords (`opus`, `vp8` or `h264`) so the CLI can infer which codec reader to use.

##### FFMPEG

We pass `-listen 1` so that FFMPEG manages the socket lifecycle for us; it will remove the socket when either of the process exits.

Video (need to disable B-frames `-bf 0` and buffered write `-max_delay 0`):

```shell
% ffmpeg -i /path/to/video.h264 \
    -bf 0 -max_delay 0 -listen 1 \
    -f h264 \
    unix:/tmp/ffmpeg-h264.sock
```

Audio (`page_duration` needs to be set to prevent premature exit due to DTX):

```shell
% ffmpeg -i /path/to/audio.ogg \
    -c:a libopus -page_duration 20000 -listen 1 \
    -f opus \
    unix:/tmp/ffmpeg-opus.sock
```

LiveKit CLI:

```shell
% ./bin/livekit-cli join-room --room myroom --identity
publisher \
  --publish unix:/tmp/ffmpeg-opus.sock \
  --publish unix:/tmp/ffmpeg-h264.sock \
  --fps 23.98
```

##### Custom application

1. In your preferred language, google how to create a Unix socket. For instance, check out this [site](https://pymotw.com/2/socket/uds.html) for Python. Once a socket is created, any other process that try to create the same socket will result in an error like `bind: address already in use`. You can now push data to the socket.

2. For debugging purpose, it's a good idea to verify that the stream is playable before pushing to LiveKit. Given H.264 data pushed to the socket `unix:/tmp/myapp-h264.sock`, we can play it using `ffplay unix:/tmp/myapp-h264.sock`. Once you're happy with the result, make sure the `ffplay` process is closed.

3. Once the stream is verified, execute the following Shell command in your chosen language:

```shell
% ./bin/livekit-cli join-room --room myroom --identity publisher \
  --publish unix:/tmp/myapp-h264.sock \
  --fps 23.98
```

4. When the UDS pipeline finishes, ensure you delete the socket `rm /tmp/myapp-h264.sock` to clean up and avoid the `bind: address already in use` error.

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
$ livekit-load-tester --url <your-url> \
    --api-key <key> --api-secret <secret> \
    --room test-room --publishers 24
```

This simulates 8 video publishers to the room, with no subscribers. Video tracks are published with simulcast, at 720p, 360p, and 180p.

#### Watch the test

Use `livekit-cli` to generate a token so you can log into the room:

```shell
$ livekit-cli create-token --join --api-key <key> --api-secret <secret> \
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
$ livekit-load-tester --url <your-url> \
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
