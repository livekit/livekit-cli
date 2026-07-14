/**
 * Minimal one-file echo agent for the `lk agent daemon` e2e test -- the Node
 * sibling of testdata/echo-agent/agent.py.
 *
 * Driven in text mode, so an LLM is the only component needed. Echoes the
 * user's text verbatim, which the test asserts on.
 */
import { type JobContext, ServerOptions, cli, defineAgent, inference, voice } from '@livekit/agents';
import 'dotenv/config';
import { fileURLToPath } from 'node:url';

export default defineAgent({
  entry: async (ctx: JobContext) => {
    const session = new voice.AgentSession({
      llm: new inference.LLM({ model: 'openai/gpt-4o-mini' }),
    });
    await session.start({
      agent: new voice.Agent({
        instructions:
          'You are an echo bot. Reply with exactly the text the user sends, verbatim, and nothing else.',
      }),
      room: ctx.room,
      // No TTS, so disable audio output or the turn crashes in the tts node.
      outputOptions: { audioEnabled: false },
    });
    await ctx.connect();
  },
});

cli.runApp(new ServerOptions({ agent: fileURLToPath(import.meta.url) }));
