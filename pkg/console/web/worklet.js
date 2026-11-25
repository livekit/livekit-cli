// The wire carries 48 kHz mono, signed 16-bit PCM in little-endian order.
const frameSamples = 960; // 20 ms

class Capture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.count = 0;
    this.sum = 0;
    this.frame = new DataView(new ArrayBuffer(frameSamples * 2));
  }

  process(inputs) {
    const mono = inputs[0]?.[0];
    if (!mono) return true;
    for (const sample of mono) {
      const s = Math.max(-1, Math.min(1, sample));
      this.frame.setInt16(this.count++ * 2, s * (s < 0 ? 32768 : 32767), true);
      this.sum += s * s;
      if (this.count === frameSamples) {
        const buffer = this.frame.buffer;
        this.port.postMessage({ pcm: buffer, level: Math.sqrt(this.sum / frameSamples) }, [buffer]);
        this.frame = new DataView(new ArrayBuffer(frameSamples * 2));
        this.count = 0;
        this.sum = 0;
      }
    }
    return true;
  }
}

class Playback extends AudioWorkletProcessor {
  constructor() {
    super();
    this.queue = [];
    this.offset = 0;
    this.flush = 0;
    this.port.onmessage = ({ data }) => {
      if (data instanceof ArrayBuffer) this.queue.push(new DataView(data));
      else if (data.flush) this.flush = data.flush;
      else if (data.clear) {
        this.queue = [];
        this.offset = 0;
        this.flush = 0;
      }
    };
  }

  process(_inputs, outputs) {
    const mono = outputs[0][0];
    let sample = 0;
    while (sample < mono.length && this.queue.length) {
      const frame = this.queue[0];
      while (sample < mono.length && this.offset + 1 < frame.byteLength) {
        mono[sample++] = frame.getInt16(this.offset, true) / 32768;
        this.offset += 2;
      }
      if (this.offset + 1 >= frame.byteLength) {
        this.queue.shift();
        this.offset = 0;
      }
    }
    mono.fill(0, sample);
    if (this.flush && !this.queue.length) {
      this.port.postMessage(this.flush);
      this.flush = 0;
    }
    return true;
  }
}

registerProcessor("capture", Capture);
registerProcessor("playback", Playback);
