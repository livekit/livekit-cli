# LiveKit CLI

This package includes command line utilities that interacts with LiveKit. It allows you to:

-   Create access tokens
-   Access LiveKit APIs, create, delete rooms, etc
-   Join a room as a participant, inspecting in-room events
-   Perform load testing, efficiently simulating real-world load

# Installation

## Mac

```shell
brew install livekit-cli
```

## Linux

```shell
curl -sSL https://get.livekit.io/cli | bash
```

## Windows

Download [latest release here](https://github.com/livekit/livekit-cli/releases/latest)

## Build from source

This repo uses [Git LFS](https://git-lfs.github.com/) for embedded video resources. Please ensure git-lfs is installed on your machine.

```shell
git clone github.com/livekit/livekit-cli
make install
```

# Usage

See `livekit-cli --help` for a complete list of subcommands.

## Publishing to a room

### Publish demo video track

To publish a demo video as a participant's track, use the following.

```shell
% livekit-cli join-room --room yourroom --identity publisher \
  --publish-demo
```

It'll publish the video track with [simulcast](https://blog.livekit.io/an-introduction-to-webrtc-simulcast-6c5f1f6402eb/), at 720p, 360p, and 180p.

### Publish media files

You can publish your own audio/video files. These tracks files need to be encoded in supported codecs.
Refer to [encoding instructions](https://github.com/livekit/server-sdk-go/tree/main#publishing-tracks-to-room)

```shell
% livekit-cli join-room --room yourroom --identity publisher \
  --publish path/to/video.ivf \
  --publish path/to/audio.ogg \
  --fps 23.98
```

This will publish the pre-encoded ivf and ogg files to the room, indicating video FPS of 23.98. Note that the FPS only affects the video; it's important to match video framerate with the source to prevent out of sync issues.

### Publish from FFmpeg

It's possible to publish any source that FFmpeg supports (including live sources such as RTSP) by using it as a transcoder.

This is done by running FFmpeg in a separate process, encoding to a Unix socket. (not available on Windows).
`livekit-cli` can then read transcoded data from the socket and publishing them to the room.

First run FFmpeg like this:

```shell
$ ffmpeg -i <video-file | rtsp://url> \
  -c:v libx264 -bsf:v h264_mp4toannexb -b:v 2M -profile:v baseline -pix_fmt yuv420p \
    -x264-params keyint=120 -max_delay 0 -bf 0 \
    -listen 1 -f h264 unix:/tmp/myvideo.sock \
  -c:a libopus -page_duration 20000 -vn \
  	-listen 1 -f opus unix:/tmp/myaudio.sock
```

This transcodes the input into H.264 baseline profile and Opus.

Then, run `livekit-cli` like this:

```shell
$ livekit-cli join-room --room yourroom --identity bot \
  --publish h264:///tmp/myvideo.sock \
  --publish opus:///tmp/myaudio.sock
````

You should now see both video and audio tracks published to the room.

### Publish from TCP (i.e. gstreamer)

It's possible to publish from video streams coming over a TCP socket. `livekit-cli` can act as a TCP client. For example, with a gstreamer pipeline ending in `! tcpserversink port=16400` and streaming H.264.

Run `livekit-cli` like this:

```shell
$ livekit-cli join-room --room yourroom --identity bot \
  --publish h264:///127.0.0.1:16400
```

### Publish streams from your application

Using unix sockets, it's also possible to publish streams from your application. The tracks need to be encoded into
a format that WebRTC clients could playback (VP8, H.264, and Opus).

Once you are writing to the socket, you could use `ffplay` to test the stream.

```shell
$ ffplay -i unix:/tmp/myvideo.sock
```

## Recording & egress

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

### Testing egress templates

In order to speed up the development cycle of your recording templates, we provide a sub-command `test-egress-template` that
helps you to validate your templates.

The command will spin up a few virtual publishers, and then simulate them joining your room
It'll then open a browser to the template URL, with the correct parameters filled in.

Here's an example:

```shell
$ livekit-cli test-egress-template
  --base-url http://localhost:3000 \
  --url <livekit-instance>
  --api-key <key>
  --api-secret <secret>
  --room <your-room> --layout <your-layout> --publishers 3
```

This command will launch a browser pointed at `http://localhost:3000`, while simulating 3 publishers publishing to your livekit instance.

## Load Testing

Load testing utility for LiveKit. This tool is quite versatile and is able to simulate various types of load.

Note: `livekit-load-tester` has been renamed to sub-command `livekit-cli load-test`

### Quickstart

This guide requires a LiveKit server instance to be set up. You can start a load tester with:

```shell
$ livekit-cli load-test --url <your-url> \
    --api-key <key> --api-secret <secret> \
    --room test-room --publishers 24
```

This simulates 8 video publishers to the room, with no subscribers. Video tracks are published with simulcast, at 720p, 360p, and 180p.

#### Watch the test

Generate a token so you can log into the room:

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
$ livekit-cli load-test --url <your-url> \
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
