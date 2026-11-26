(function attachVoiceCore(root, factory) {
  const voiceCore = factory();
  if (typeof module !== "undefined" && module.exports) {
    module.exports = voiceCore;
  } else {
    root.JarvisVoiceCore = voiceCore;
  }
})(globalThis, () => {
  const TARGET_SAMPLE_RATE = 16000;
  const VOICE_PREFERENCE_DEFAULTS = Object.freeze({
    startAtLogin: true,
    listeningEnabled: true,
    muted: false,
    whisperModel: "base",
    microphoneDeviceId: "",
    proactiveSuggestionsEnabled: true,
    doNotDisturb: false,
    quietHoursStart: "22:00",
    quietHoursEnd: "08:00",
    proactiveCooldownMinutes: 30,
    lastSuggestionAt: 0,
    announceTaskCompletions: true,
    announceReminders: true,
  });

  function normalizeSpeechText(value) {
    return sanitizeTranscript(value)
      .normalize("NFKC")
      .toLocaleLowerCase()
      .replace(/[.,!?،؛؟।]/g, " ")
      .replace(/\s+/g, " ")
      .trim();
  }

  function sanitizeTranscript(value) {
    return String(value || "")
      .normalize("NFKC")
      .replace(/[\u0000-\u001f\u007f-\u009f\u200b\u200e\u200f\u202a-\u202e\u2060\u2066-\u2069\ufeff]/gu, " ")
      .replace(/\s+/gu, " ")
      .trim();
  }

  function normalizeCommand(value) {
    return sanitizeTranscript(value);
  }

  function isValidTranscript(value) {
    const text = sanitizeTranscript(value);
    const words = text.match(/[\p{L}\p{N}]+/gu) || [];
    if (!text || text.length > 1000 || words.length === 0 || words.length > 100) {
      return false;
    }
    if (/^(?:thanks?\s+for\s+watching|please\s+subscribe|subtitles?\s+by)\b/iu.test(text)) {
      return false;
    }
    for (let size = 1; size <= Math.min(4, Math.floor(words.length / 3)); size += 1) {
      if (words.length % size !== 0 || words.length / size < 3) {
        continue;
      }
      const pattern = words.slice(0, size).join(" ").toLocaleLowerCase();
      if (Array.from({ length: words.length / size }, (_, index) =>
        words.slice(index * size, (index + 1) * size).join(" ").toLocaleLowerCase(),
      ).every((part) => part === pattern)) {
        return false;
      }
    }
    return true;
  }

  function isDuplicateTranscript(value, previousValue, elapsedMs, windowMs = 2500) {
    const current = normalizeSpeechText(value);
    return Boolean(current && current === normalizeSpeechText(previousValue) &&
      Number.isFinite(elapsedMs) && elapsedMs >= 0 && elapsedMs < windowMs);
  }

  function normalizeVoicePreferences(saved = {}) {
    const preferences = { ...VOICE_PREFERENCE_DEFAULTS };
    for (const key of ["startAtLogin", "listeningEnabled", "muted", "proactiveSuggestionsEnabled",
      "doNotDisturb", "announceTaskCompletions", "announceReminders"]) {
      if (typeof saved[key] === "boolean") {
        preferences[key] = saved[key];
      }
    }
    if (saved.whisperModel === "base" || saved.whisperModel === "small") {
      preferences.whisperModel = saved.whisperModel;
    }
    if (typeof saved.microphoneDeviceId === "string" && saved.microphoneDeviceId.length <= 512) {
      preferences.microphoneDeviceId = saved.microphoneDeviceId;
    }
    for (const key of ["quietHoursStart", "quietHoursEnd"]) {
      if (typeof saved[key] === "string" && /^([01]\d|2[0-3]):[0-5]\d$/u.test(saved[key])) {
        preferences[key] = saved[key];
      }
    }
    if (Number.isInteger(saved.proactiveCooldownMinutes) &&
        saved.proactiveCooldownMinutes >= 1 && saved.proactiveCooldownMinutes <= 1440) {
      preferences.proactiveCooldownMinutes = saved.proactiveCooldownMinutes;
    }
    if (Number.isFinite(saved.lastSuggestionAt) && saved.lastSuggestionAt >= 0) {
      preferences.lastSuggestionAt = saved.lastSuggestionAt;
    }
    return preferences;
  }

  function updateVoicePreference(current, key, value) {
    if (!Object.hasOwn(VOICE_PREFERENCE_DEFAULTS, key)) {
      throw new Error("Unsupported JARVIS voice preference.");
    }
    const next = normalizeVoicePreferences({ ...current, [key]: value });
    if (next[key] !== value) {
      throw new Error(`Invalid value for JARVIS preference ${key}.`);
    }
    return next;
  }

  function isWithinQuietHours(date, start, end) {
    if (!/^([01]\d|2[0-3]):[0-5]\d$/u.test(start) ||
        !/^([01]\d|2[0-3]):[0-5]\d$/u.test(end) || start === end) {
      return false;
    }
    const currentMinutes = date.getHours() * 60 + date.getMinutes();
    const toMinutes = (time) => Number(time.slice(0, 2)) * 60 + Number(time.slice(3));
    const startMinutes = toMinutes(start);
    const endMinutes = toMinutes(end);
    return startMinutes < endMinutes
      ? currentMinutes >= startMinutes && currentMinutes < endMinutes
      : currentMinutes >= startMinutes || currentMinutes < endMinutes;
  }

  function shouldSpeakProactive(preferences, kind, now = new Date(), lastSuggestionAt = 0) {
    const date = now instanceof Date ? now : new Date(now);
    if (preferences.doNotDisturb ||
        isWithinQuietHours(date, preferences.quietHoursStart, preferences.quietHoursEnd)) {
      return false;
    }
    if (kind === "reminder") {
      return preferences.announceReminders;
    }
    if (kind === "task") {
      return preferences.announceTaskCompletions;
    }
    if (kind === "suggestion") {
      return preferences.proactiveSuggestionsEnabled &&
        date.getTime() - lastSuggestionAt >= preferences.proactiveCooldownMinutes * 60_000;
    }
    return false;
  }

  function completionSuggestion(task) {
    const description = sanitizeTranscript(task?.description).toLocaleLowerCase();
    const output = sanitizeTranscript(task?.output).toLocaleLowerCase();
    if (description.startsWith("reminder:") ||
        !/\b(created|saved|updated|generated|completed|ready)\b/u.test(output)) {
      return "";
    }
    return "Would you like me to review or open the completed result?";
  }

  function analyzeAudio(samples) {
    if (!(samples instanceof Float32Array) || samples.length === 0) {
      throw new TypeError("audio samples are required");
    }
    let sumSquares = 0;
    let peak = 0;
    let clippedSamples = 0;
    let zeroCrossings = 0;
    for (let index = 0; index < samples.length; index += 1) {
      const sample = samples[index];
      sumSquares += sample * sample;
      peak = Math.max(peak, Math.abs(sample));
      if (Math.abs(sample) >= 0.995) {
        clippedSamples += 1;
      }
      if (index > 0 && ((sample >= 0) !== (samples[index - 1] >= 0))) {
        zeroCrossings += 1;
      }
    }
    const rms = Math.sqrt(sumSquares / samples.length);
    return {
      rms,
      peak,
      decibels: rms > 0 ? 20 * Math.log10(rms) : -Infinity,
      clippedRatio: clippedSamples / samples.length,
      zeroCrossingRate: zeroCrossings / samples.length,
    };
  }

  function isSpeechLikeAudio(samples, sampleRate = TARGET_SAMPLE_RATE) {
    if (!(samples instanceof Float32Array) || sampleRate <= 0 ||
        samples.length < sampleRate * 0.25) {
      return false;
    }
    const stats = analyzeAudio(samples);
    if (stats.rms < 0.006 || stats.peak < 0.015 || stats.clippedRatio > 0.12 ||
        stats.zeroCrossingRate < 0.002 || stats.zeroCrossingRate > 0.48) {
      return false;
    }
    const frameSize = Math.max(80, Math.floor(sampleRate * 0.01));
    const energies = [];
    for (let offset = 0; offset + frameSize <= samples.length; offset += frameSize) {
      let sumSquares = 0;
      for (let index = offset; index < offset + frameSize; index += 1) {
        sumSquares += samples[index] * samples[index];
      }
      const frameRms = Math.sqrt(sumSquares / frameSize);
      if (frameRms >= 0.004) {
        energies.push(frameRms);
      }
    }
    if (energies.length < 8) {
      return false;
    }
    const mean = energies.reduce((total, value) => total + value, 0) / energies.length;
    const variance = energies.reduce((total, value) => total + (value - mean) ** 2, 0) / energies.length;
    return mean > 0 && Math.sqrt(variance) / mean >= 0.04;
  }

  async function resumeAudioContext(context, timeoutMs = 3000) {
    if (!context || context.state === "closed") {
      throw new Error("The microphone AudioContext is unavailable or closed.");
    }
    if (context.state !== "running") {
      let timeout;
      try {
        await Promise.race([
          context.resume(),
          new Promise((resolve, reject) => {
            timeout = setTimeout(
              () => reject(new Error("AudioContext resume timed out; click Retry microphone.")),
              timeoutMs,
            );
          }),
        ]);
      } finally {
        clearTimeout(timeout);
      }
    }
    if (context.state !== "running") {
      throw new Error(`The microphone AudioContext is ${context.state}; audio capture cannot proceed.`);
    }
    if (!Number.isFinite(context.sampleRate) || context.sampleRate <= 0) {
      throw new Error("The microphone AudioContext reported an invalid sample rate.");
    }
    return context.sampleRate;
  }

  function shouldRestoreListeningStatus({ listening, voiceState, processing, failed }) {
    return listening && voiceState !== "Speaking" && !processing && !failed;
  }

  class StreamingSpeechQueue {
    constructor({
      speak,
      interrupt = () => {},
      onFirstSpeak = () => {},
      onError = () => {},
      maxSentences = 12,
      maxQueuedCharacters = 2400,
      maxSentenceCharacters = 320,
    } = {}) {
      if (typeof speak !== "function") {
        throw new TypeError("a speech function is required");
      }
      this.speak = speak;
      this.interrupt = interrupt;
      this.onFirstSpeak = onFirstSpeak;
      this.onError = onError;
      this.maxSentences = maxSentences;
      this.maxQueuedCharacters = maxQueuedCharacters;
      this.maxSentenceCharacters = maxSentenceCharacters;
      this.buffer = "";
      this.queue = [];
      this.pumping = null;
      this.active = false;
      this.closed = false;
      this.cancelled = false;
      this.hasContent = false;
      this.firstSpeakSent = false;
    }

    push(delta) {
      if (this.closed || this.cancelled) {
        return false;
      }
      if (typeof delta !== "string") {
        throw new TypeError("streamed speech must be text");
      }
      this.buffer += delta;
      this.hasContent = this.hasContent || Boolean(delta.trim());
      this.extractSentences(false);
      if (this.queue.length + (this.active ? 1 : 0) > this.maxSentences ||
          this.buffer.length + this.queue.reduce((total, sentence) => total + sentence.length, 0) >
            this.maxQueuedCharacters) {
        this.fail(new Error("Speech queue limit reached; remaining text is available on screen."));
        return false;
      }
      this.pump();
      return true;
    }

    async finish() {
      if (this.cancelled) {
        return;
      }
      this.closed = true;
      const remaining = this.buffer.trim();
      this.buffer = "";
      if (remaining) {
        this.queue.push(remaining);
      }
      if (this.queue.length + (this.active ? 1 : 0) > this.maxSentences ||
          this.queue.reduce((total, sentence) => total + sentence.length, 0) >
            this.maxQueuedCharacters) {
        this.fail(new Error("Speech queue limit reached; remaining text is available on screen."));
        return;
      }
      this.pump();
      while (this.pumping) {
        await this.pumping;
      }
    }

    cancel() {
      if (this.cancelled) {
        return Promise.resolve(false);
      }
      this.cancelled = true;
      this.closed = true;
      this.buffer = "";
      this.queue = [];
      if (this.active) {
        return Promise.resolve(this.interrupt())
          .then(() => true)
          .catch((error) => {
            this.onError(error);
            return false;
          });
      }
      return Promise.resolve(true);
    }

    extractSentences(force) {
      while (this.buffer) {
        let boundary = -1;
        for (let index = 0; index < this.buffer.length; index += 1) {
          if (!/[.!?。？！।॥]/u.test(this.buffer[index])) {
            continue;
          }
          let end = index + 1;
          while (/["'”’)\]]/u.test(this.buffer[end] || "")) {
            end += 1;
          }
          if (end < this.buffer.length && /\s/u.test(this.buffer[end])) {
            boundary = end;
            break;
          }
          if (force && end === this.buffer.length) {
            boundary = end;
            break;
          }
        }
        if (boundary !== -1) {
          const sentence = this.buffer.slice(0, boundary).trim();
          this.buffer = this.buffer.slice(boundary).trimStart();
          if (sentence) {
            this.queue.push(sentence);
          }
          continue;
        }
        if (this.buffer.length <= this.maxSentenceCharacters) {
          break;
        }
        let splitAt = this.buffer.lastIndexOf(" ", this.maxSentenceCharacters);
        if (splitAt <= 0) {
          splitAt = this.maxSentenceCharacters;
        }
        const sentence = this.buffer.slice(0, splitAt).trim();
        this.buffer = this.buffer.slice(splitAt).trimStart();
        if (sentence) {
          this.queue.push(sentence);
        }
      }
    }

    pump() {
      if (this.pumping || this.cancelled || this.queue.length === 0) {
        return;
      }
      const run = async () => {
        while (this.queue.length > 0 && !this.cancelled) {
          const sentence = this.queue.shift();
          this.active = true;
          if (!this.firstSpeakSent) {
            this.firstSpeakSent = true;
            this.onFirstSpeak();
          }
          try {
            await this.speak(sentence);
          } catch (error) {
            this.fail(error);
          } finally {
            this.active = false;
          }
        }
      };
      const pumping = run().finally(() => {
        if (this.pumping === pumping) {
          this.pumping = null;
          if (this.queue.length > 0 && !this.cancelled) {
            this.pump();
          }
        }
      });
      this.pumping = pumping;
    }

    fail(error) {
      if (this.cancelled) {
        return;
      }
      this.onError(error);
      this.cancel();
    }
  }

  function extractWakeCommand(value) {
    const text = String(value || "").normalize("NFKC");
    // Whisper's base model often drops "Hey" and writes Jarvis as Zavis.
    const pattern = /(?:^|[\s,.!?।])(?:(?:hey|hi|okay|ok)\s+|(?:हे|है)\s*)?(?:jarvis|jervis|jarvice|javis|jarves|zavis|जार्विस)(?=$|[\s,.!?।])/iu;
    const match = pattern.exec(text);
    if (!match) {
      return { activated: false, command: "" };
    }
    return {
      activated: true,
      command: text.slice(match.index + match[0].length)
        .replace(/^[\s,.:;!?।-]+/, "")
        .replace(/[.।]+$/u, "")
        .trim(),
    };
  }

  function resolveConversationCommand(value, conversationActive) {
    const wake = extractWakeCommand(value);
    if (!conversationActive && !wake.activated) {
      return null;
    }
    return wake.activated ? wake.command : String(value || "").trim();
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

  class StreamingResampler {
    constructor(inputRate, outputRate = TARGET_SAMPLE_RATE) {
      if (!Number.isFinite(inputRate) || inputRate <= 0 ||
          !Number.isFinite(outputRate) || outputRate <= 0) {
        throw new TypeError("resampler rates must be positive numbers");
      }
      this.inputRate = inputRate;
      this.outputRate = outputRate;
      this.step = inputRate / outputRate;
      this.buffer = [];
      this.buffered = 0;
      this.readIndex = 0;
    }

    push(input) {
      if (!(input instanceof Float32Array) || input.length === 0) {
        return new Float32Array(0);
      }
      for (const sample of input) {
        this.buffer.push(sample);
      }
      this.buffered += input.length;
      const output = [];
      while (this.readIndex + 1 < this.buffered) {
        const before = Math.floor(this.readIndex);
        const fraction = this.readIndex - before;
        const first = this.buffer[before];
        const second = this.buffer[before + 1];
        output.push(first * (1 - fraction) + second * fraction);
        this.readIndex += this.step;
      }
      this.compact();
      return Float32Array.from(output);
    }

    compact() {
      if (this.buffered === 0) {
        return;
      }
      const keepFrom = Math.max(0, Math.min(this.buffered - 2, Math.floor(this.readIndex) - 1));
      if (keepFrom <= 0) {
        return;
      }
      this.buffer = this.buffer.slice(keepFrom);
      this.buffered -= keepFrom;
      this.readIndex -= keepFrom;
    }

    flush() {
      const output = [];
      while (this.readIndex < this.buffered) {
        const before = Math.min(Math.floor(this.readIndex), this.buffered - 1);
        const fraction = Math.min(1, this.readIndex - before);
        const first = this.buffer[before];
        const second = this.buffer[Math.min(before + 1, this.buffered - 1)];
        output.push(first * (1 - fraction) + second * fraction);
        this.readIndex += this.step;
      }
      this.buffer = [];
      this.buffered = 0;
      this.readIndex = 0;
      return Float32Array.from(output);
    }
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
      this.threshold = options.threshold ?? 0.012;
      this.silenceSamples = Math.floor(this.sampleRate * (options.silenceMs || 700) / 1000);
      this.minimumSamples = Math.floor(this.sampleRate * (options.minimumMs || 180) / 1000);
      this.maximumSamples = Math.floor(this.sampleRate * (options.maximumMs || 12000) / 1000);
      this.preRollSamples = Math.floor(this.sampleRate * (options.preRollMs || 240) / 1000);
      this.lastFrameVoiced = false;
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
      this.lastFrameVoiced = false;
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
      this.lastFrameVoiced = voiced;

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
    VOICE_PREFERENCE_DEFAULTS,
    StreamingResampler,
    StreamingSpeechQueue,
    VoiceOnsetDetector,
    VoiceActivitySegmenter,
    analyzeAudio,
    completionSuggestion,
    encodeWav,
    extractWakeCommand,
    isDuplicateTranscript,
    isSpeechLikeAudio,
    isValidTranscript,
    isWithinQuietHours,
    normalizeCommand,
    sanitizeTranscript,
    normalizeVoicePreferences,
    normalizeSpeechText,
    resolveConversationCommand,
    resemblesEcho,
    resampleTo16k,
    resumeAudioContext,
    shouldRestoreListeningStatus,
    shouldSpeakProactive,
    updateVoicePreference,
  };
});
