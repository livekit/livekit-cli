<!--BEGIN_BANNER_IMAGE-->

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="/.github/banner_dark.png">
  <source media="(prefers-color-scheme: light)" srcset="/.github/banner_light.png">
  <img style="width:100%;" alt="The LiveKit icon, the name of the repository and some sample code in the background." src="https://raw.githubusercontent.com/livekit/livekit-cli/main/.github/banner_light.png">
</picture>

<!--END_BANNER_IMAGE-->

# LiveKit CLI

<!--BEGIN_DESCRIPTION--><!--END_DESCRIPTION-->

This package includes command line utilities that interacts with LiveKit. It allows you to:

-   Bootstrap new applications from templates
-   Create access tokens
-   Access LiveKit APIs, create and delete rooms, etc.
-   Join a room as a participant, inspecting in-room events
-   Start and manage Egresses
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

Or download a precompiled binary for the [latest release](https://github.com/livekit/livekit-cli/releases/latest)

## Windows

```shell
winget install LiveKit.LiveKitCLI
```

Or download a precompiled binary for the [latest release](https://github.com/livekit/livekit-cli/releases/latest)

## Build from source

This repo uses [Git LFS](https://git-lfs.github.com/) for embedded video resources. Please ensure git-lfs is installed on your machine.

```shell
git clone https://github.com/livekit/livekit-cli
make install
```

# Usage

See `lk --help` for a complete list of subcommands. The `--help` flag can also be used on any subcommand for more information.

## Set up your project (new)

The quickest way to get started is to authenticate with your LiveKit Cloud account and link an existing project. If you haven't created an account or project yet, head [here](https://cloud.livekit.io) first. Then run the following:

```shell
lk cloud auth
```

Follow the URL to login in the browser and choose your project to link. When you return to your terminal, you'll have the option to use this project as the default.

When a default project is set up, you can omit `url`, `api-key`, and `api-secret` when using the CLI. You can also set up multiple projects, and temporarily switch the active project used with the `--project` flag, or persistently using `lk project set-default`.

### Adding a project manually

```shell
lk project add --api-key <key> --api-secret <secret> <project_name>
```
### Listing projects

```shell
lk project list
```

### Switching defaults
    
```shell
lk project set-default <project_name>
```

## Bootstrapping an application

The LiveKit CLI can help you bootstrap applications from a number of convenient template repositories, using your project credentials to set up required environment variables and other configuration automatically. To create an application from a template, run the following:

```shell
lk app create --template <template_name> my-app
```

Then follow the CLI prompts to finish your setup.

For a list of all available templates, run:

```shell
lk app list-templates
```

See the [LiveKit Templates Index](https://github.com/livekit-examples/index?tab=readme-ov-file) for details about templates, and for instructions on how to contribute your own.

## Publishing to a room

### Join a room and set participant attributes

```shell
lk room join --identity publisher \
  --attribute key1=value1 \
  --attribute key2=value2 \
  <room_name>
```

You can also specify attributes by providing a JSON file:

```shell
lk room join --identity publisher \
  --attribute-file attributes.json \
  <room_name>
```

Where `attributes.json` contains a JSON object with key-value pairs:

```json
{
  "key1": "value1",
  "key2": "value2"
}
```

### Publish demo video track

To publish a demo video as a participant's track, use the following:

```shell
lk room join --identity publisher --publish-demo <room_name>
```

This will publish the demo video track with [simulcast](https://blog.livekit.io/an-introduction-to-webrtc-simulcast-6c5f1f6402eb/), at 720p, 360p, and 180p.

### Publish media files

You can publish your own audio/video files. These tracks files need to be encoded in supported codecs.
Refer to [encoding instructions](https://github.com/livekit/server-sdk-go/tree/main#publishing-tracks-to-room)

```shell
lk room join --identity publisher \
  --publish <path/to/video.ivf> \
  --publish <path/to/audio.ogg> \
  --fps 23.98 \
  <room_name>
```

This will publish the pre-encoded `.ivf` and `.ogg` files to the room, indicating video FPS of 23.98. Note that the FPS only affects the video; it's important to match video framerate with the source to prevent out of sync issues.

Note: For files uploaded via CLI, expect an initial delay before the video becomes visible to the remote viewer. This delay is attributed to the pre-encoded video's fixed keyframe intervals. Video encoded with LiveKit client SDKs do not have this delay.

### Publish from FFmpeg

It's possible to publish any source that FFmpeg supports (including live sources such as RTSP) by using it as a transcoder.

This is done by running FFmpeg in a separate process, encoding to a Unix socket. (not available on Windows).
`lk` can then read transcoded data from the socket and publishing them to the room.

First run FFmpeg like this:

```shell
ffmpeg -i <video-file | rtsp://url> \
  -c:v libx264 -bsf:v h264_mp4toannexb -b:v 2M -profile:v baseline -pix_fmt yuv420p \
    -x264-params keyint=120 -max_delay 0 -bf 0 \
    -listen 1 -f h264 unix:/tmp/myvideo.sock \
  -c:a libopus -page_duration 20000 -vn \
  	-listen 1 -f opus unix:/tmp/myaudio.sock
```

This transcodes the input into H.264 baseline profile and Opus.

Then, run `lk` like this:

```shell
lk room join --identity bot \
  --publish h264:///tmp/myvideo.sock \
  --publish opus:///tmp/myaudio.sock \
  <room_name>
````

You should now see both video and audio tracks published to the room.

### Publish from TCP (i.e. gstreamer)

It's possible to publish from video streams coming over a TCP socket. `lk` can act as a TCP client. For example, with a gstreamer pipeline ending in `! tcpserversink port=16400` and streaming H.264.

Run `lk` like this:

```shell
lk room join --identity bot \
  --publish h264:///127.0.0.1:16400 \
  <room_name>
```

### Publish streams from your application

Using unix sockets, it's also possible to publish streams from your application. The tracks need to be encoded into
a format that WebRTC clients could playback (VP8, H.264, and Opus).

Once you are writing to the socket, you could use `ffplay` to test the stream.

```shell
ffplay -i unix:/tmp/myvideo.sock
```

## Recording & egress

Recording requires [egress service](https://docs.livekit.io/guides/egress/) to be set up first.

Example request.json files are [located here](https://github.com/livekit/livekit-cli/tree/main/cmd/lk/examples).

```shell
# Start room composite (recording of room UI)
lk egress start --type room-composite <path/to/request.json>

# Start track composite (audio + video)
lk egress start --type track-composite <path/to/request.json>

# Start track egress (single audio or video track)
lk egress start --type track <path/to/request.json>
```

### Testing egress templates

In order to speed up the development cycle of your recording templates, we provide a sub-command `test-egress-template` that
helps you to validate your templates.

The command will spin up a few virtual publishers, and then simulate them joining your room
It'll then open a browser to the template URL, with the correct parameters filled in.

Here's an example:

```shell
lk egress test-template \
  --base-url http://localhost:3000 \
  --room test-room --layout speaker --video-publishers 3
```

This command will launch a browser pointed at `http://localhost:3000`, while simulating 3 publishers publishing to your livekit instance.

## Load Testing

Load testing utility for LiveKit. This tool is quite versatile and is able to simulate various types of load.

Note: `livekit-load-tester` has been renamed to sub-command `lk load-test`

### Quickstart

This guide requires a LiveKit server instance to be set up. You can start a load tester with:

```shell
lk load-test \
  --room test-room --video-publishers 8
```

This simulates 8 video publishers to the room, with no subscribers. Video tracks are published with simulcast, at 720p, 360p, and 180p.

#### Simulating audio publishers

To test audio capabilities in your app, you can also simulate simultaneous speakers to the room.

```shell
lk load-test \
  --room test-room --audio-publishers 5
```

The above simulates 5 concurrent speakers, each playing back a pre-recorded audio sample at the same time.
In a meeting, typically there's only one active speaker at a time, but this can be useful to test audio capabilities.

#### Watch the test

Generate a token so you can log into the room:

```shell
lk token create --join \
  --room test-room --identity test-user
```

Head over to the [example web client](https://meet.livekit.io/?tab=custom) and paste in the token, you can see the simulated tracks published by the load tester.

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
lk load-test \
  --duration 1m \
  --video-publishers 5 \
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

You can customize various parameters of the test such as

-   `--video-publishers`: number of video publishers
-   `--audio-publishers`: number of audio publishers
-   `--subscribers`: number of subscribers
-   `--video-resolution`: publishing video resolution. low, medium, high
-   `--no-simulcast`: disables simulcast
-   `--num-per-second`: number of testers to start each second
-   `--layout`: layout to simulate (speaker, 3x3, 4x4, or 5x5)
-   `--simulate-speakers`: randomly rotate publishers to speak

### Agent Load Testing

The agent load testing utility allows you to dispatch a running agent to a number of rooms and simulate a user in each room that would echo whatever the agent says. 

> **Note**: Before running the test, ensure that:
> - Your agent is already running using `start` instead of `dev` with the specified `agent_name` configured
> - The agent is configured to speak something first (e.g., a simple greeting)

To start an agent load test:

```shell
lk agent-load-test \
  --rooms 5 \
  --agent-name test-agent \
  --echo-speech-delay 10s \
  --duration 5m
```

The above simulates 5 concurrent rooms, where each room has:
- Your agent `test-agent` dispatched
- An echo participant that receives and plays back the agent's audio
- A 10-second delay in the echo response from the agent speech
- The test runs for 5 minutes before automatically stopping

Once the specified duration is over (or if the load test is manually stopped), the load test statistics will be displayed in the form of a table.

<!--BEGIN_REPO_NAV-->
<br/><table>
<thead><tr><th colspan="2">LiveKit Ecosystem</th></tr></thead>
<tbody>
<tr><td>LiveKit SDKs</td><td><a href="https://github.com/livekit/client-sdk-js">Browser</a> · <a href="https://github.com/livekit/client-sdk-swift">iOS/macOS/visionOS</a> · <a href="https://github.com/livekit/client-sdk-android">Android</a> · <a href="https://github.com/livekit/client-sdk-flutter">Flutter</a> · <a href="https://github.com/livekit/client-sdk-react-native">React Native</a> · <a href="https://github.com/livekit/rust-sdks">Rust</a> · <a href="https://github.com/livekit/node-sdks">Node.js</a> · <a href="https://github.com/livekit/python-sdks">Python</a> · <a href="https://github.com/livekit/client-sdk-unity">Unity</a> · <a href="https://github.com/livekit/client-sdk-unity-web">Unity (WebGL)</a></td></tr><tr></tr>
<tr><td>Server APIs</td><td><a href="https://github.com/livekit/node-sdks">Node.js</a> · <a href="https://github.com/livekit/server-sdk-go">Golang</a> · <a href="https://github.com/livekit/server-sdk-ruby">Ruby</a> · <a href="https://github.com/livekit/server-sdk-kotlin">Java/Kotlin</a> · <a href="https://github.com/livekit/python-sdks">Python</a> · <a href="https://github.com/livekit/rust-sdks">Rust</a> · <a href="https://github.com/agence104/livekit-server-sdk-php">PHP (community)</a> · <a href="https://github.com/pabloFuente/livekit-server-sdk-dotnet">.NET (community)</a></td></tr><tr></tr>
<tr><td>UI Components</td><td><a href="https://github.com/livekit/components-js">React</a> · <a href="https://github.com/livekit/components-android">Android Compose</a> · <a href="https://github.com/livekit/components-swift">SwiftUI</a></td></tr><tr></tr>
<tr><td>Agents Frameworks</td><td><a href="https://github.com/livekit/agents">Python</a> · <a href="https://github.com/livekit/agents-js">Node.js</a> · <a href="https://github.com/livekit/agent-playground">Playground</a></td></tr><tr></tr>
<tr><td>Services</td><td><a href="https://github.com/livekit/livekit">LiveKit server</a> · <a href="https://github.com/livekit/egress">Egress</a> · <a href="https://github.com/livekit/ingress">Ingress</a> · <a href="https://github.com/livekit/sip">SIP</a></td></tr><tr></tr>
<tr><td>Resources</td><td><a href="https://docs.livekit.io">Docs</a> · <a href="https://github.com/livekit-examples">Example apps</a> · <a href="https://livekit.io/cloud">Cloud</a> · <a href="https://docs.livekit.io/home/self-hosting/deployment">Self-hosting</a> · <b>CLI</b></td></tr>
</tbody>
</table>
<!--END_REPO_NAV-->
