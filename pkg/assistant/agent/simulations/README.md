# LK simulations

These run LK, the agent behind `lk assistant`, through [Agent Simulations](https://docs.livekit.io/testing/simulations.md): an LLM plays the user, and a judge grades the conversation against each scenario's expectations.

Under a simulation, `lk_simulation.py` mocks lk's tools, such as `create_project`, with replies like the ones lk sends. The docs tools stay real. Each scenario's `userdata` sets the folder context, and its `expect` block lists tool calls that `check_simulation` checks when the simulation ends, because the judge doesn't see tool calls. lk doesn't embed `lk_simulation.py` or this folder.

## Run

From `pkg/assistant/agent`, with a LiveKit Cloud project configured in `lk`:

```shell
uv sync
lk agent simulate text --scenarios simulations/scenarios.yaml --samples 3 -y
lk agent simulate audio --scenarios simulations/audio-scenarios.yaml --samples 3 -y
```

`--samples` must be odd, and at most 3.

## Settings to compare

Set these environment variables to compare configurations. The defaults are what lk ships.

| Variable | Default | Values |
|---|---|---|
| `LK_ASSISTANT_LLM` | `openai/gpt-5.4-mini` | A LiveKit Inference LLM, such as `google/gemini-3.8-flash` |
| `LK_ASSISTANT_MAX_PAGES` | `3` | How many docs pages one reply may read. Fewer is faster. |
| `LK_ASSISTANT_DOCS_CHARS` | `12000` | How much docs text one `read_docs` call returns, split between its pages. |
| `LK_ASSISTANT_TOC` | `1` | `1` adds the trimmed docs table of contents to the instructions, so the agent picks pages by title. |
| `LK_ASSISTANT_REASONING` | `low` | The LLM's `reasoning_effort`, such as `low` or `medium`. Empty uses the model's default. Lower is faster. |

Each run registers its own temporary agent name, so you can run two configurations at once.
