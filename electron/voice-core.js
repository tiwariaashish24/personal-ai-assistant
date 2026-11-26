(function attachVoiceCore(root, factory) {
  const voiceCore = factory();
  if (typeof module !== "undefined" && module.exports) {
    module.exports = voiceCore;
  } else {
    root.JarvisVoiceCore = voiceCore;
  }
})(globalThis, () => {
  const TARGET_SAMPLE_RATE = 16000;

  function normalizeSpeechText(value) {
    return String(value || "")
      .normalize("NFKC")
      .toLocaleLowerCase()
      .replace(/[.,!?،؛؟।]/g, " ")
      .replace(/\s+/g, " ")
      .trim();
  }

  function extractWakeCommand(value) {
    const text = String(value || "").normalize("NFKC");
    const pattern = /(?:^|[\s,!?।])(?:hey\s+jarvis|हे\s*जार्विस)(?=$|[\s,!?।])/iu;
    const match = pattern.exec(text);
    if (!match) {
      return { activated: false, command: "" };
    }
    return {
      activated: true,
      command: (text.slice(0, match.index) + text.slice(match.index + match[0].length))
        .replace(/^[\s,.:;!?।-]+/, "")
        .trim(),
    };
  }

  function resampleTo16k(input, inputRate) {
    if (!(input instanceof Float32Array) || inputRate <= 0) {
      throw new TypeError("audio samples and a positive sample rate are required");
    }
    if (inputRate === TARGET_SAMPLE_RATE) {
      return input.slice();
    }
    const outputLength = Math.floor(input.length * TARGET_SAMPLE_RATE / inputRate);
    const output = new Float32Array(outputLength);
    const scale = inputRate / TARGET_SAMPLE_RATE;
    for (let index = 0; index < outputLength; index += 1) {
      const position = index * scale;
      const before = Math.floor(position);
      const after = Math.min(before + 1, input.length - 1);
      const fraction = position - before;
      output[index] = input[before] * (1 - fraction) + input[after] * fraction;
    }
    return output;
  }

  function encodeWav(samples, sampleRate = TARGET_SAMPLE_RATE) {
    if (!(samples instanceof Float32Array) || sampleRate <= 0) {
      throw new TypeError("audio samples and a positive sample rate are required");
    }
    const buffer = new ArrayBuffer(44 + samples.length * 2);
    const view = new DataView(buffer);
    const writeText = (offset, text) => {
      for (let index = 0; index < text.length; index += 1) {
        view.setUint8(offset + index, text.charCodeAt(index));
      }
    };
    writeText(0, "RIFF");
    view.setUint32(4, 36 + samples.length * 2, true);
    writeText(8, "WAVE");
    writeText(12, "fmt ");
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, sampleRate, true);
    view.setUint32(28, sampleRate * 2, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    writeText(36, "data");
    view.setUint32(40, samples.length * 2, true);
    for (let index = 0; index < samples.length; index += 1) {
      const sample = Math.max(-1, Math.min(1, samples[index]));
      view.setInt16(44 + index * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
    }
    return buffer;
  }

  class VoiceActivitySegmenter {
    constructor(options = {}) {
      this.sampleRate = options.sampleRate || TARGET_SAMPLE_RATE;
      this.threshold = options.threshold || 0.018;
      this.silenceSamples = Math.floor(this.sampleRate * (options.silenceMs || 700) / 1000);
      this.minimumSamples = Math.floor(this.sampleRate * (options.minimumMs || 300) / 1000);
      this.maximumSamples = Math.floor(this.sampleRate * (options.maximumMs || 12000) / 1000);
      this.preRollSamples = Math.floor(this.sampleRate * (options.preRollMs || 240) / 1000);
      this.reset();
    }

    reset() {
      this.preRoll = [];
      this.preRollLength = 0;
      this.frames = [];
      this.frameLength = 0;
      this.voicedSamples = 0;
      this.silentLength = 0;
      this.active = false;
    }

    push(samples) {
      if (!(samples instanceof Float32Array)) {
        throw new TypeError("voice activity input must be Float32Array");
      }
      if (samples.length === 0) {
        return null;
      }
      let sum = 0;
      for (const sample of samples) {
        sum += sample * sample;
      }
      const voiced = Math.sqrt(sum / samples.length) >= this.threshold;

      if (!this.active) {
        this.keepPreRoll(samples);
        if (!voiced) {
          return null;
        }
        this.active = true;
        this.frames = this.preRoll.map((frame) => frame.slice());
        this.frameLength = this.preRollLength;
        this.voicedSamples = samples.length;
        this.silentLength = 0;
      } else {
        this.frames.push(samples.slice());
        this.frameLength += samples.length;
        if (voiced) {
          this.voicedSamples += samples.length;
          this.silentLength = 0;
        } else {
          this.silentLength += samples.length;
        }
      }

      if (this.frameLength >= this.maximumSamples || this.silentLength >= this.silenceSamples) {
        return this.finish();
      }
      return null;
    }

    keepPreRoll(samples) {
      const copy = samples.slice();
      this.preRoll.push(copy);
      this.preRollLength += copy.length;
      while (this.preRollLength > this.preRollSamples && this.preRoll.length > 1) {
        this.preRollLength -= this.preRoll.shift().length;
      }
    }

    finish() {
      let result = null;
      if (this.voicedSamples >= this.minimumSamples) {
        result = new Float32Array(this.frameLength);
        let offset = 0;
        for (const frame of this.frames) {
          result.set(frame, offset);
          offset += frame.length;
        }
      }
      this.reset();
      return result;
    }
  }

  class VoiceOnsetDetector {
    constructor(options = {}) {
      this.sampleRate = options.sampleRate || TARGET_SAMPLE_RATE;
      this.threshold = options.threshold || 0.025;
      this.minimumSamples = Math.floor(this.sampleRate * (options.minimumMs || 180) / 1000);
      this.reset();
    }

    reset() {
      this.voicedSamples = 0;
      this.triggered = false;
    }

    push(samples) {
      if (!(samples instanceof Float32Array)) {
        throw new TypeError("voice onset input must be Float32Array");
      }
      if (samples.length === 0) {
        return false;
      }
      let sum = 0;
      for (const sample of samples) {
        sum += sample * sample;
      }
      if (Math.sqrt(sum / samples.length) < this.threshold) {
        this.reset();
        return false;
      }
      this.voicedSamples += samples.length;
      if (!this.triggered && this.voicedSamples >= this.minimumSamples) {
        this.triggered = true;
        return true;
      }
      return false;
    }
  }

  function resemblesEcho(transcript, spokenText) {
    const heard = normalizeSpeechText(transcript);
    const spoken = normalizeSpeechText(spokenText);
    if (!heard || !spoken) {
      return false;
    }
    if (spoken.includes(heard)) {
      return true;
    }
    const heardWords = heard.split(" ");
    if (heardWords.length < 3) {
      return heard === spoken;
    }
    const spokenWords = new Set(spoken.split(" "));
    const matches = heardWords.filter((word) => spokenWords.has(word)).length;
    return matches / heardWords.length >= 0.8;
  }

  return {
    TARGET_SAMPLE_RATE,
    VoiceOnsetDetector,
    VoiceActivitySegmenter,
    encodeWav,
    extractWakeCommand,
    normalizeSpeechText,
    resemblesEcho,
    resampleTo16k,
  };
});
