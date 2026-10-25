// GoChat's strong noise suppression: RNNoise over the microphone, in an
// audio worklet. Worklets cannot fetch, so the page hands it rnnoise.wasm,
// whose two imports it answers in place of Emscripten's loader.
"use strict";

// RNNoise takes 10 ms frames of 48 kHz audio, scaled as 16-bit samples.
const FRAME = 480;
const SCALE = 32768;

class Denoiser extends AudioWorkletProcessor {
  constructor(options) {
    super();
    try {
      let memory = null;
      const env = {
        // Grow the heap to a size, then copy within it.
        a: function (size) {
          try {
            memory.grow(Math.ceil((size - memory.buffer.byteLength) / 65536));
            return 1;
          } catch (e) {
            return 0;
          }
        },
        b: function (dest, src, num) {
          new Uint8Array(memory.buffer).copyWithin(dest, src, src + num);
        }
      };
      const module = new WebAssembly.Module(options.processorOptions.wasm);
      this.wasm = new WebAssembly.Instance(module, { a: env }).exports;
      memory = this.wasm.c;
      this.wasm.d();
      this.state = this.wasm.f(0);
      this.ptr = this.wasm.g(FRAME * 4);
    } catch (e) {
      this.port.postMessage({ error: String(e) });
      this.failed = true;
      return;
    }
    this.frame = new Float32Array(FRAME);
    this.filled = 0;
    // Output trails input by a frame, so a whole one is always ready.
    this.ring = new Float32Array(FRAME * 4);
    this.read = 0;
    this.queued = FRAME;
    this.port.postMessage({ ready: true });
  }

  process(inputs, outputs) {
    if (this.failed) return false;
    const input = inputs[0][0];
    const output = outputs[0][0];
    const n = output ? output.length : 128;
    for (let i = 0; i < n; i++) {
      this.frame[this.filled++] = input ? input[i] * SCALE : 0;
      if (this.filled === FRAME) this.denoise();
    }
    for (let i = 0; i < n; i++) {
      if (output) output[i] = this.ring[this.read];
      this.read = (this.read + 1) % this.ring.length;
    }
    this.queued -= n;
    return true;
  }

  denoise() {
    const heap = new Float32Array(this.wasm.c.buffer, this.ptr, FRAME);
    heap.set(this.frame);
    this.wasm.j(this.state, this.ptr, this.ptr);
    let at = (this.read + this.queued) % this.ring.length;
    for (let i = 0; i < FRAME; i++) {
      this.ring[at] = heap[i] / SCALE;
      at = (at + 1) % this.ring.length;
    }
    this.queued += FRAME;
    this.filled = 0;
  }
}

registerProcessor("gochat-denoise", Denoiser);
