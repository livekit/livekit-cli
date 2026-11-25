const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const status = $("status");
const error = $("error");
let textMode = document.body.dataset.textMode === "true";
let ws, context, mic, source, capture, playback, analyser;
let ready = false, muted = false, ending = false;
let requestID = 0, pendingTurn = "", flushID = 0, pendingFlush = 0;
let agentState = "starting", userSpeaking = false, micLevel = 0, partial;
let logTimer;

// The ring follows the agent's actual playback, or animates lightly in text mode.
const canvas = $("viz");
const drawing = canvas.getContext("2d");
const bars = new Float32Array(72);
const spectrum = new Uint8Array(64);
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
const darkMode = matchMedia("(prefers-color-scheme: dark)");
let size = 0, scale = 1, ink, you;

function updateColors() {
  const style = getComputedStyle(document.documentElement);
  ink = style.getPropertyValue("--text").trim();
  you = style.getPropertyValue("--you").trim();
}
updateColors();
darkMode.addEventListener("change", updateColors);
new ResizeObserver(() => {
  size = canvas.clientWidth;
  scale = devicePixelRatio || 1;
  canvas.width = canvas.height = Math.round(size * scale);
}).observe(canvas);

function draw(ms) {
  const t = ms / 1000;
  const calm = reducedMotion.matches ? 0.25 : 1;
  const listening = userSpeaking && agentState !== "speaking";
  const hasPlayback = analyser && !textMode;
  if (hasPlayback && agentState === "speaking") analyser.getByteFrequencyData(spectrum);
  drawing.setTransform(scale, 0, 0, scale, 0, 0);
  drawing.clearRect(0, 0, size, size);
  drawing.lineCap = "round";
  drawing.lineWidth = size * 0.0125;
  drawing.strokeStyle = listening ? you : ink;
  const center = size / 2;
  const radius = size * 0.27;

  for (let i = 0; i < bars.length; i++) {
    const angle = (i / bars.length) * Math.PI * 2;
    let target = 0.05 + 0.035 * Math.sin(t * 1.1 + i * 0.35) * calm;
    if (agentState === "speaking") {
      const bin = Math.floor(Math.abs((i / bars.length) * 2 - 1) * 38) + 1;
      target = hasPlayback ? Math.pow(spectrum[bin] / 255, 1.25) * 1.05
        : 0.3 + 0.25 * Math.sin(t * 9 + i * 0.8) * calm;
    } else if (agentState === "thinking") {
      const offset = angle - (t * 2.2) % (Math.PI * 2);
      const distance = Math.abs(Math.atan2(Math.sin(offset), Math.cos(offset)));
      target = 0.08 + Math.max(0, 0.55 - distance * 0.9) * calm;
    } else if (listening) {
      target += micLevel * 1.6 * (0.6 + 0.4 * Math.sin(i * 0.9 + t * 7));
    }
    bars[i] += (target - bars[i]) * 0.28;
    const value = Math.min(1, bars[i]);
    const length = size * (0.012 + value * 0.2);
    const x = Math.sin(angle);
    const y = -Math.cos(angle);
    drawing.globalAlpha = 0.28 + value * 0.72;
    drawing.beginPath();
    drawing.moveTo(center + x * radius, center + y * radius);
    drawing.lineTo(center + x * (radius + length), center + y * (radius + length));
    drawing.stroke();
  }
  if (!$("ended").hidden) return;
  requestAnimationFrame(draw);
}
requestAnimationFrame(draw);

function send(message) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message));
}

function showError(text) {
  error.textContent = text;
  error.hidden = !text;
}

function setStatus(text, className = "") {
  status.textContent = text;
  $("dot").className = `dot ${className}`;
}

function setAgentState(state) {
  agentState = state;
  $("state").textContent = ready ? state.charAt(0).toUpperCase() + state.slice(1) : "";
}

function caption(text, className = "") {
  $("caption").textContent = text;
  $("caption").className = className;
}

function append(className, text, meta) {
  const list = $("transcript");
  const line = document.createElement("li");
  line.dir = "auto";
  line.className = className;
  line.textContent = text;
  if (meta) {
    const detail = document.createElement("span");
    detail.className = "meta";
    detail.textContent = meta;
    line.append(detail);
  }
  list.append(line);
  list.scrollTop = list.scrollHeight;
  return line;
}

function renderMode() {
  $("composer").hidden = !textMode;
  $("mute").hidden = textMode;
  $("mode").classList.toggle("on", textMode);
  const label = textMode ? "Switch to voice" : "Switch to typing";
  $("mode").setAttribute("aria-label", label);
  $("mode").title = label;
  if (ready && textMode) $("message").focus();
}

async function startAudio() {
  if (!navigator.mediaDevices?.getUserMedia) {
    throw new Error("Microphone access is unavailable here. Open the local console in a regular browser tab.");
  }
  const existing = context;
  // Request permission directly in the click, before awaiting audio setup.
  const permission = navigator.mediaDevices.getUserMedia({
    audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
  });
  let audio = existing;
  try {
    audio ??= new AudioContext({ sampleRate: 48000, latencyHint: "interactive" });
    if (audio.sampleRate !== 48000) throw new Error("this browser can't run audio at 48 kHz");
    const [stream] = await Promise.all([permission, audio.resume()]);
    if (!existing) await audio.audioWorklet.addModule("/static/worklet.js");
    if (ending || (ws && ws.readyState !== WebSocket.OPEN)) {
      throw new Error("the session ended");
    }
    if (!existing) {
      capture = new AudioWorkletNode(audio, "capture", { numberOfOutputs: 0 });
      playback = new AudioWorkletNode(audio, "playback", { outputChannelCount: [1] });
      analyser = audio.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.6;
      playback.connect(analyser);
      analyser.connect(audio.destination);
      capture.port.onmessage = ({ data: { pcm, level } }) => {
        micLevel += (level - micLevel) * 0.4;
        $("lvl").style.setProperty("--lvl", Math.min(1, micLevel * 5));
        $("lvl").style.opacity = muted ? 0 : Math.min(1, micLevel * 8);
        if (ready && !textMode && ws?.readyState === WebSocket.OPEN && ws.bufferedAmount < 32768) {
          ws.send(pcm);
        }
      };
      playback.port.onmessage = ({ data }) => {
        if (data === pendingFlush) finishPlayback();
      };
    }
    source = audio.createMediaStreamSource(stream);
    source.connect(capture);
    mic = stream;
    mic.getAudioTracks().forEach((track) => (track.enabled = !muted));
    context = audio;
  } catch (err) {
    void permission.then((stream) => stream.getTracks().forEach((track) => track.stop()), () => {});
    if (!existing && audio) void audio.close();
    throw err;
  }
}

function microphoneError(err) {
  if (err.name === "NotAllowedError" || err.name === "SecurityError") {
    return "Microphone access wasn’t granted. Open the site controls next to the address, set Microphone to Allow, then press Start talking again.";
  }
  if (err.name === "NotFoundError") return "No microphone was found. Plug one in and try again, or use typing.";
  if (err.name === "NotReadableError") return "The microphone is unavailable. Check Windows microphone privacy settings or close other apps using it, then try again.";
  return err.message || "Could not start the microphone.";
}

function stopMic() {
  source?.disconnect();
  source = undefined;
  mic?.getTracks().forEach((track) => track.stop());
  mic = undefined;
  micLevel = 0;
}

function finishPlayback() {
  pendingFlush = 0;
  send({ type: "playback_finished" });
}

function onEvent(event) {
  const state = event.agent_state_changed;
  if (state) {
    setAgentState((state.new_state ?? "AS_INITIALIZING").replace(/^AS_/, "").toLowerCase());
  }
  if (event.user_state_changed) {
    userSpeaking = (event.user_state_changed.new_state ?? "US_SPEAKING") === "US_SPEAKING";
    $("youspeaking").hidden = !userSpeaking;
  }
  if (event.user_input_transcribed && !textMode) {
    const text = event.user_input_transcribed.transcript ?? "";
    if (!partial) partial = append("msg user partial", text);
    partial.textContent = text;
    caption(text, "you partial");
  }
  const item = event.conversation_item_added?.item;
  if (item?.message) {
    const message = item.message;
    const text = (message.content ?? []).map((part) => part.text ?? "").join("");
    if (text && message.role === "USER") {
      partial?.remove();
      partial = undefined;
      append("msg user", text);
      caption(text, "you");
    }
    if (text && message.role === "ASSISTANT") {
      const metrics = message.metrics ?? {};
      const timing = [
        ["end of turn", metrics.end_of_turn_delay], ["LLM first token", metrics.llm_node_ttft],
        ["TTS first byte", metrics.tts_node_ttfb], ["total", metrics.e2e_latency],
      ].filter(([, value]) => typeof value === "number").map(([label, value]) => `${label} ${value.toFixed(2)}s`);
      if (message.interrupted) timing.unshift("interrupted");
      append("msg assistant", text, timing.join(" · "));
      caption(text);
    }
  }
  if (item?.function_call) {
    append("note tool", `🔧 ${item.function_call.name}(${item.function_call.arguments ?? ""})`);
  }
  if (item?.function_call_output?.output) append("note tool", `↳ ${item.function_call_output.output}`);
  if (item?.agent_handoff) append("note", item.agent_handoff.old_agent_id ? "Handed off to another agent" : "Agent started");
  if (event.error?.message) showError(event.error.message);
}

function connect() {
  const url = new URL("/ws", location.href);
  url.protocol = "ws:";
  url.searchParams.set("k", params.get("k"));
  if (textMode) url.searchParams.set("text", "1");
  ws = new WebSocket(url);
  ws.binaryType = "arraybuffer";
  setStatus("connecting");
  $("welcometext").textContent = "Connecting…";
  ws.onmessage = ({ data }) => {
    if (typeof data !== "string") {
      if (!textMode) playback?.port.postMessage(data, [data]);
      return;
    }
    const message = JSON.parse(data);
    switch (message.type) {
      case "ready":
        ready = true;
        setStatus("connected", "live");
        $("welcome").hidden = true;
        $("session").hidden = false;
        setAgentState("listening");
        renderMode();
        break;
      case "event":
        onEvent(message.event);
        break;
      case "response":
        if (message.response.request_id === pendingTurn) {
          pendingTurn = "";
          $("send").disabled = false;
          $("message").focus();
        }
        if (message.response.error) showError(message.response.error);
        break;
      case "flush":
        if (textMode) finishPlayback();
        else playback.port.postMessage({ flush: (pendingFlush = ++flushID) });
        break;
      case "clear":
        pendingFlush = 0;
        playback?.port.postMessage({ clear: true });
        break;
    }
  };
  ws.onclose = ({ wasClean }) => {
    const wasReady = ready;
    ready = false;
    stopMic();
    void context?.close();
    setDrawer(false);
    $("session").hidden = true;
    $("welcome").hidden = true;
    $("youspeaking").hidden = true;
    setStatus("disconnected", "bad");
    $("ended").hidden = false;
    $("endedtitle").textContent = ending || (wasReady && wasClean) ? "Session ended" : "Disconnected";
    $("endedtext").textContent = ending || (wasReady && wasClean)
      ? "You can close this tab."
      : "Check the terminal and open the URL printed by lk. Only one tab can connect.";
  };
}

$("start").onclick = async () => {
  $("start").disabled = true;
  $("start-text").disabled = true;
  showError("");
  setStatus("waiting for microphone");
  $("welcometext").textContent = "Choose Allow in the browser’s microphone prompt.";
  try {
    await startAudio();
    $("start").hidden = true;
    $("start-text").hidden = true;
    connect();
  } catch (err) {
    $("start").disabled = false;
    $("start-text").disabled = false;
    $("welcometext").textContent = "Allow your microphone to start talking, or switch to typing.";
    setStatus("microphone unavailable", "bad");
    showError(microphoneError(err));
  }
};

$("start-text").onclick = () => {
  textMode = true;
  showError("");
  $("start").hidden = true;
  $("start-text").hidden = true;
  connect();
};

$("mode").onclick = async () => {
  $("mode").disabled = true;
  showError("");
  try {
    if (textMode) {
      await startAudio();
      textMode = false;
    } else {
      textMode = true;
      stopMic();
      userSpeaking = false;
      $("youspeaking").hidden = true;
      partial?.remove();
      partial = undefined;
      playback?.port.postMessage({ clear: true });
      if (pendingFlush) finishPlayback();
    }
    send({ type: "update_io", id: `io-${++requestID}`, audio: !textMode });
    renderMode();
  } catch (err) {
    if (ready) showError(microphoneError(err));
  } finally {
    $("mode").disabled = false;
  }
};

$("mute").onclick = () => {
  muted = !muted;
  mic?.getAudioTracks().forEach((track) => (track.enabled = !muted));
  $("mute").setAttribute("aria-pressed", muted);
  $("mute").classList.toggle("off", muted);
  const label = muted ? "Unmute microphone" : "Mute microphone";
  $("mute").setAttribute("aria-label", label);
  $("mute").title = label;
};

$("composer").onsubmit = (event) => {
  event.preventDefault();
  const input = $("message");
  const text = input.value.trim();
  if (!ready || !text || pendingTurn) return;
  showError("");
  input.value = "";
  pendingTurn = `run-${++requestID}`;
  $("send").disabled = true;
  send({ type: "run_input", id: pendingTurn, text });
};

$("end").onclick = () => {
  ending = true;
  ws.close(1000);
};

async function loadLogs() {
  try {
    const response = await fetch(`/logs?k=${encodeURIComponent(params.get("k"))}`);
    if (!response.ok) throw new Error();
    $("agentlog").textContent = (await response.json()).join("\n");
    $("logs").scrollTop = $("logs").scrollHeight;
  } catch {
    $("agentlog").textContent = "Could not load logs. Check the log path printed in the terminal.";
  }
}

function watchLogs() {
  clearInterval(logTimer);
  if (!$("drawer").inert && !$("logs").hidden) {
    void loadLogs();
    logTimer = setInterval(loadLogs, 1500);
  }
}

function setDrawer(open) {
  $("drawer").inert = !open;
  $("drawer").classList.toggle("open", open);
  $("chat").setAttribute("aria-expanded", open);
  if (open) $("closedrawer").focus();
  else if (ready) $("chat").focus();
  watchLogs();
}

function selectTab(logs) {
  $("tab-chat").setAttribute("aria-selected", !logs);
  $("tab-logs").setAttribute("aria-selected", logs);
  $("transcript").hidden = logs;
  $("logs").hidden = !logs;
  watchLogs();
}

$("chat").onclick = () => setDrawer($("drawer").inert);
$("closedrawer").onclick = () => setDrawer(false);
$("tab-chat").onclick = () => selectTab(false);
$("tab-logs").onclick = () => selectTab(true);
addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !$("drawer").inert) setDrawer(false);
});

if (textMode) connect();
else {
  $("welcometext").textContent = "Press the button to open your microphone.";
  $("start").hidden = false;
  $("start-text").hidden = false;
}
