# LiveKit CLI

A command line utility to interact with LiveKit. `livekit-cli` allows you to:

- Access LiveKit APIs, create, delete rooms, etc
- Create access tokens
- Join a room as a participant, verifying in-room events are getting fired

## Installation



## Usage

## livekit-cli

```shell
% ./bin/livekit-cli --help
NAME:
   livekit-cli - CLI client to LiveKit

USAGE:
   livekit-cli [global options] command [command options] [arguments...]

VERSION:
   0.5.1

COMMANDS:
   create-token        creates an access token
   create-room
   list-rooms
   delete-room
   list-participants
   get-participant
   remove-participant
   mute-track
   join-room           joins a room as a client
   help, h             Shows a list of commands or help for one command
```

## livekit-load-tester

```shell
% ./bin/livekit-load-tester --help
NAME:
   livekit-cli - LiveKit load tester

USAGE:
   livekit-load-tester [global options] command [command options] [arguments...]

VERSION:
   0.5.0

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --url value              URL of LiveKit server [$LIVEKIT_URL]
   --api-key value           [$LIVEKIT_API_KEY]
   --api-secret value        [$LIVEKIT_API_SECRET]
   --room value             name of the room
   --duration value         duration to run, 1m, 1h, 0 to keep running (default: 0s)
   --max-latency value      max number of subscribers without going above max latency (e.g. 400ms) (default: 0s)
   --publishers value       number of participants to publish tracks to the room (default: 0)
   --subscribers value      number of participants to join the room (default: 0)
   --identity-prefix value  identity prefix of tester participants, defaults to a random name
   --video-bitrate value    bitrate (bps) of video track to publish, 0 to disable (default: 1000000)
   --audio-bitrate value    bitrate (bps) of audio track to publish, 0 to disable (default: 20000)
   --expected-tracks value  total number of expected tracks in the room; use for multi-node tests (default: 0)
   --run-all                runs set list of load test cases (default: false)
   --help, -h               show help (default: false)
   --version, -v            print the version (default: false)
```

### Load test results

* server: gke, c2-standard-8
* network latency: 7.3ms
* audio bitrate: 20kbps
* video bitrate: 1mbps

| Publishers | Subscribers | Audio | Video | Tracks | Latency | Packet loss
|---         |---          |---    |---    |---     |---      |---
| 1          | 1           | Yes   | No    | 1      | 47.9ms  | 0.0000%
| 9          | 0           | Yes   | No    | 72     | 46.6ms  | 0.0000%
| 9          | 0           | Yes   | Yes   | 144    | 47.2ms  | 0.0059%
| 9          | 100         | Yes   | No    | 972    | 47.6ms  | 0.0002%
| 50         | 0           | Yes   | No    | 2450   | 47.7ms  | 0.0005%
| 9          | 100         | Yes   | Yes   | 1944   | 104.8ms | 0.0001%
| 9          | 500         | Yes   | No    | 4572   | 186.9ms | 0.0010%
| 50         | 0           | Yes   | Yes   | 4900   | 324.8ms | 0.0034%
| 9          | 500         | Yes   | Yes   | 9144   | 363.2ms | 0.0002%
| 100        | 0           | Yes   | No    | 9900   | 368.9ms | 0.0002%
| 5          | 1000        | Yes   | Yes   | 10040  | 381.8ms | 0.0002%
| 10         | 1000        | Yes   | No    | 10090  | 384.0ms | 0.0001%
