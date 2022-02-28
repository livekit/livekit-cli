# LiveKit CLI & Load Tester

This package includes command line utilities that interacts with LiveKit. It allows you to:

- Create access tokens
- Access LiveKit APIs, create, delete rooms, etc
- Join a room as a participant, inspecting in-room events
- Perform load testing, efficiently simulating real-world load

## Installation

```shell
$ go install github.com/livekit/livekit-cli/cmd/livekit-cli@latest
$ go install github.com/livekit/livekit-cli/cmd/livekit-load-tester@latest
```

## Usage

## livekit-cli

```shell
% ./bin/livekit-cli --help
USAGE:
   livekit-cli [global options] command [command options] [arguments...]

VERSION:
   0.7.0

COMMANDS:
   create-token          creates an access token
   create-room           
   list-rooms            
   delete-room           
   list-participants     
   get-participant       
   remove-participant    
   mute-track            
   update-subscriptions  
   join-room             joins a room as a client
   start-recording       Starts a recording with a deployed recorder service
   add-output            Adds an rtmp output url to a live recording
   remove-output         Removes an rtmp output url from a live recording
   end-recording         Ends a recording
   help, h               Shows a list of commands or help for one command
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
  --publish path/to/video.ivf \
  --publish path/to/audio.ogg \
  --fps 23.98
```

This will publish the pre-encoded ivf and ogg files to the room, indicating video FPS of 23.98. 

### Recording

Recording requires a [recorder service](https://docs.livekit.io/guides/recording/#service) to be set up first.

```shell
% ./bin/livekit-cli start-recording --help
NAME:
   livekit-cli start-recording - starts a recording with a deployed recorder service

USAGE:
   livekit-cli start-recording [command options] [arguments...]

OPTIONS:
   --url value         url to LiveKit instance (default: "http://localhost:7880") [$LIVEKIT_URL]
   --api-key value      [$LIVEKIT_API_KEY]
   --api-secret value   [$LIVEKIT_API_SECRET]
   --request value     StartRecordingRequest as json file (see https://github.com/livekit/livekit-recorder#request)
   --help, -h          show help (default: false)
```

Sample `request` json file:

```json
{
    "template": {
        "layout": "speaker-dark",
        "token": "token"
    },
    "s3_url": "s3://bucket/path/filename.mp4"
}
```

## livekit-load-tester

Load testing utility for LiveKit. This tool is quite versatile and is able to simulate various types of load.

### Quickstart

This guide requires a LiveKit server instance to be set up. You can start a load tester with:

```shell
$ ./livekit-load-tester --url wss://<your-url> --api-key <key> --api-secret <secret> --room test-room --publishers 8
```

This simulates 8 video publishers to the room, with no subscribers. Video tracks are published with simulcast, at 720p, 360p, and 180p.

#### Watch the test

Use `livekit-cli` to generate a token so you can log into the room:

```shell
$ ./livekit-cli create-token --join --api-key <key> --api-secret <secret> --room test-room --identity user  
```

Head over to the [example web client](https://example.livekit.io) and paste in the token, you can see the fake tracks published by the load tester.

![Load tester screenshot](misc/load-test-screenshot.png?raw=true)

### Configuring system settings

Prior to running the load tests, it's important to ensure file descriptor limits have been set correctly. See [Performance tuning docs](https://docs.livekit.io/deploy/test-monitor#performance-tuning).

On the machine that you are running the load tester, they would also need to be applied:

```shell
ulimit -n 65535
sysctl -w net.core.rmem_max=25165824
sysctl -w fs.file-max=2097152
sysctl -w net.core.somaxconn=65535
sysctl -w net.core.netdev_max_backlog=65535
sysctl -w net.core.optmem_max=25165824
sysctl -w net.core.rmem_max=25165824
sysctl -w net.core.wmem_max=25165824
sysctl -w net.core.rmem_default=1048576
sysctl -w net.core.wmem_default=1048576
```

### Simulate subscribers

You can run the load tester on multiple machines, each simulating any number of publishers or subscribers.

LiveKit SFU's performance is [measured by](https://docs.livekit.io/deploy/benchmark#measuring-performance) the amount
of data sent to its subscribers.

Use this command to simulate a load test of 5 publishers, and 500 subscribers:

```shell
$ ./livekit-load-tester --url wss://<your-instance> \
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

* --publishers: number of publishers
* --subscribers: number of publishers
* --audio-bitrate: bitrate of audio track
* --video-bitrate: bitrate of video track
* --no-simulcast: disables simulcast
* --num-per-second: number of testers to start each second
* --layout: layout to simulate (speaker, 3x3, 4x4, or 5x5)
