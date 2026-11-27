const test = require("node:test");
const assert = require("node:assert/strict");
const {
  VoiceActivitySegmenter,
  VoiceOnsetDetector,
  StreamingSpeechQueue,
  analyzeAudio,
  completionSuggestion,
  encodeWav,
  extractWakeCommand,
  isDuplicateTranscript,
  isSpeechLikeAudio,
  isValidTranscript,
  isWithinQuietHours,
  normalizeCommand,
  normalizeVoicePreferences,
  resumeAudioContext,
  resolveConversationCommand,
  resemblesEcho,
  resampleTo16k,
  shouldRestoreListeningStatus,
  shouldSpeakProactive,
  updateVoicePreference,
} = require("./voice-core.js");

test("wake phrase activates only on the explicit phrase and preserves its command", () => {
  assert.deepEqual(extractWakeCommand("hey jarvis, turn on the lights"), {
    activated: true,
    command: "turn on the lights",
  });
  assert.deepEqual(extractWakeCommand("हे जार्विस कैलेंडर खोलो"), {
    activated: true,
    command: "कैलेंडर खोलो",
  });
  assert.deepEqual(extractWakeCommand("Hey Zavis, open chrome."), {
    activated: true,
    command: "open chrome",
  });
  assert.deepEqual(extractWakeCommand("Hi Jarvis."), {
    activated: true,
    command: "",
  });
  assert.deepEqual(extractWakeCommand("Jarvis, open my calendar"), {
    activated: true,
    command: "open my calendar",
  });
  assert.deepEqual(extractWakeCommand("Here is Jarvis Open Chrome."), {
    activated: true,
    command: "Open Chrome",
  });
  assert.deepEqual(extractWakeCommand("He's Jarvis, open YouTube."), {
    activated: true,
    command: "open YouTube",
  });
  assert.deepEqual(extractWakeCommand("Here's our WIS Open Chrome."), {
    activated: true,
    command: "Open Chrome",
  });
  assert.deepEqual(extractWakeCommand("Please Arvis open chrome."), {
    activated: true,
    command: "open chrome",
  });
  assert.equal(extractWakeCommand("I told Jarvis to open chrome.").activated, false);
  for (const ignored of [
    "What are you doing?",
    "I think it will be a good idea to do something.",
    "Have a nice day.",
    "Thanks for being here.",
    "This is what we are going to do. We will do it.",
    "Can you name a hotel at base?",
  ]) {
    assert.equal(extractWakeCommand(ignored).activated, false, ignored);
  }
  assert.equal(extractWakeCommand("My thing point is, Jarpoint didn't give you the answer.").activated, false);
  assert.equal(extractWakeCommand("He is always open chrome and then YouTube.").activated, false);
  assert.equal(extractWakeCommand("Open my calendar").activated, false);
  assert.equal(extractWakeCommand("These are with open chrome.").activated, false);
});

test("conversation accepts follow-ups without a wake phrase only during an active session", () => {
  assert.equal(resolveConversationCommand("What is next?", false), null);
  assert.equal(resolveConversationCommand("Hey Jarvis, what is next?", false), "what is next?");
  assert.equal(resolveConversationCommand("What is next?", true), "What is next?");
});

test("voice activity segmenter keeps pre-roll and ends after silence", () => {
  const segmenter = new VoiceActivitySegmenter({
    sampleRate: 1000,
    threshold: 0.05,
    silenceMs: 200,
    minimumMs: 100,
    preRollMs: 100,
  });
  segmenter.push(new Float32Array(50).fill(0));
  segmenter.push(new Float32Array(50).fill(0.1));
  segmenter.push(new Float32Array(100).fill(0.1));
  const utterance = segmenter.push(new Float32Array(200).fill(0));
  assert.equal(utterance.length, 400);
  assert.equal(utterance[0], 0);
  assert.ok(Math.abs(utterance[100] - 0.1) < 1e-6);
});

test("short, low-level wake speech survives VAD and transcript gating", () => {
  const segmenter = new VoiceActivitySegmenter({ sampleRate: 16000, silenceMs: 100 });
  let utterance;
  for (let frame = 0; frame < 24; frame += 1) {
    const samples = new Float32Array(160);
    for (let index = 0; index < samples.length; index += 1) {
      const time = (frame * samples.length + index) / 16000;
      const envelope = 0.035 + 0.018 * Math.sin(2 * Math.PI * 4 * time);
      samples[index] = envelope * Math.sin(2 * Math.PI * 180 * time);
    }
    utterance = segmenter.push(samples) || utterance;
  }
  for (let frame = 0; frame < 10; frame += 1) {
    utterance = segmenter.push(new Float32Array(160)) || utterance;
  }
  assert.ok(utterance);
  assert.ok(utterance.length >= 16000 * 0.25);
  assert.equal(isSpeechLikeAudio(utterance), true);
  assert.equal(isValidTranscript("Hey Jarvis"), true);
  assert.equal(resolveConversationCommand("Hey Jarvis", false), "");
});

test("suspended microphone AudioContext is resumed and invalid state is surfaced", async () => {
  const context = {
    state: "suspended",
    sampleRate: 48000,
    resume: async function resume() {
      this.state = "running";
    },
  };
  assert.equal(await resumeAudioContext(context), 48000);
  await assert.rejects(
    resumeAudioContext({ state: "closed", sampleRate: 16000, resume: async () => {} }),
    /unavailable or closed/,
  );
  await assert.rejects(
    resumeAudioContext({ state: "suspended", sampleRate: 16000, resume: async () => {} }),
    /cannot proceed/,
  );
  await assert.rejects(
    resumeAudioContext({ state: "suspended", sampleRate: 16000, resume: () => new Promise(() => {}) }, 5),
    /resume timed out/,
  );
});

test("transcription failures are not replaced with the standby status", () => {
  assert.equal(shouldRestoreListeningStatus({
    listening: true,
    voiceState: "Error",
    processing: false,
    failed: true,
  }), false);
  assert.equal(shouldRestoreListeningStatus({
    listening: true,
    voiceState: "Thinking",
    processing: false,
    failed: false,
  }), true);
});

test("voice onset detector requires sustained speech and resets on silence", () => {
  const detector = new VoiceOnsetDetector({
    sampleRate: 1000,
    threshold: 0.05,
    minimumMs: 100,
  });
  const speech = new Float32Array(50).fill(0.1);
  assert.equal(detector.push(speech), false);
  assert.equal(detector.push(speech), true);
  assert.equal(detector.push(speech), false);
  assert.equal(detector.push(new Float32Array(20)), false);
  assert.equal(detector.push(speech), false);
});

test("voice WAV encoding produces mono 16-bit PCM", () => {
  const wav = encodeWav(new Float32Array([-1, 0, 1]), 16000);
  const view = new DataView(wav);
  assert.equal(String.fromCharCode(...new Uint8Array(wav, 0, 4)), "RIFF");
  assert.equal(String.fromCharCode(...new Uint8Array(wav, 8, 4)), "WAVE");
  assert.equal(view.getUint16(22, true), 1);
  assert.equal(view.getUint16(34, true), 16);
  assert.equal(view.getInt16(44, true), -32768);
  assert.equal(view.getInt16(48, true), 32767);
});

test("audio resampling and echo detection handle supported speech input", () => {
  const input = new Float32Array([0, 1, 0, -1]);
  assert.equal(resampleTo16k(input, 32000).length, 2);
  const nativeRateSpeech = Float32Array.from(
    { length: 4800 },
    (_, index) => 0.1 * Math.sin(2 * Math.PI * 440 * index / 48000),
  );
  const resampledSpeech = resampleTo16k(nativeRateSpeech, 48000);
  assert.equal(resampledSpeech.length, 1600);
  assert.ok(analyzeAudio(resampledSpeech).rms > 0.06);
  assert.equal(resemblesEcho("Your reminder is ready", "Your reminder is ready now"), true);
  assert.equal(resemblesEcho("Cancel that", "Your reminder is ready now"), false);
});

test("streaming speech speaks complete sentences early and drains the bounded queue", async () => {
  const spoken = [];
  let releaseFirst;
  const queue = new StreamingSpeechQueue({
    speak: (sentence) => {
      spoken.push(sentence);
      if (spoken.length === 1) {
        return new Promise((resolve) => {
          releaseFirst = resolve;
        });
      }
      return Promise.resolve();
    },
  });
  queue.push("First sentence. ");
  assert.deepEqual(spoken, ["First sentence."]);
  queue.push("Second sentence!");
  const finished = queue.finish();
  releaseFirst();
  await finished;
  assert.deepEqual(spoken, ["First sentence.", "Second sentence!"]);
});

test("interrupting streaming speech clears queued sentences and cancels active playback", async () => {
  const spoken = [];
  let releaseSpeech;
  let interruptCount = 0;
  const queue = new StreamingSpeechQueue({
    speak: (sentence) => {
      spoken.push(sentence);
      return new Promise((resolve) => {
        releaseSpeech = resolve;
      });
    },
    interrupt: () => {
      interruptCount += 1;
      releaseSpeech();
    },
  });
  queue.push("First. ");
  queue.push("Second. ");
  const finished = queue.finish();
  queue.cancel();
  await finished;
  assert.deepEqual(spoken, ["First."]);
  assert.equal(interruptCount, 1);
});

test("streaming speech queue reports its capacity limit instead of dropping text silently", () => {
  const errors = [];
  const queue = new StreamingSpeechQueue({
    speak: async () => {},
    onError: (error) => errors.push(error.message),
    maxQueuedCharacters: 8,
  });
  assert.equal(queue.push("a response that is too long"), false);
  assert.deepEqual(errors, ["Speech queue limit reached; remaining text is available on screen."]);
});

test("streaming speech queue enforces sentence capacity when flushing its final buffer", async () => {
  const spoken = [];
  const errors = [];
  let releaseSpeech;
  const queue = new StreamingSpeechQueue({
    speak: (sentence) => {
      spoken.push(sentence);
      return new Promise((resolve) => {
        releaseSpeech = resolve;
      });
    },
    interrupt: () => releaseSpeech(),
    onError: (error) => errors.push(error.message),
    maxSentences: 1,
  });
  queue.push("First. ");
  queue.push("Second sentence");
  await queue.finish();
  assert.deepEqual(spoken, ["First."]);
  assert.deepEqual(errors, ["Speech queue limit reached; remaining text is available on screen."]);
});

test("multilingual transcripts are preserved and filtered for invalid or repeated hallucinations", () => {
  const original = "  हे जार्विस,   कैलेंडर खोलो  ";
  assert.equal(normalizeCommand(original), "हे जार्विस, कैलेंडर खोलो");
  assert.equal(isValidTranscript(original), true);
  assert.equal(isValidTranscript("thanks for watching"), false);
  assert.equal(isValidTranscript("jarvis jarvis jarvis"), false);
  assert.equal(isDuplicateTranscript("Open calendar", "open calendar.", 500), true);
  assert.equal(isDuplicateTranscript("Open calendar", "open calendar", 3000), false);
});

test("audio diagnostics reject silence and steady tones while retaining speech-like audio", () => {
  const silence = new Float32Array(16000);
  const tone = new Float32Array(16000);
  const speechLike = new Float32Array(16000);
  for (let index = 0; index < tone.length; index += 1) {
    const time = index / 16000;
    tone[index] = 0.1 * Math.sin(2 * Math.PI * 200 * time);
    const envelope = 0.1 + 0.04 * Math.sin(2 * Math.PI * 4 * time);
    speechLike[index] = envelope * Math.sin(2 * Math.PI * 180 * time);
  }
  assert.equal(analyzeAudio(silence).decibels, -Infinity);
  assert.equal(isSpeechLikeAudio(silence), false);
  assert.equal(isSpeechLikeAudio(tone), false);
  assert.equal(isSpeechLikeAudio(speechLike), true);
});

test("proactive communication respects quiet hours, do-not-disturb, preferences, and cooldowns", () => {
  const preferences = normalizeVoicePreferences({});
  assert.equal(isWithinQuietHours(new Date(2026, 0, 1, 23, 0), "22:00", "08:00"), true);
  assert.equal(isWithinQuietHours(new Date(2026, 0, 1, 12, 0), "22:00", "08:00"), false);
  assert.equal(shouldSpeakProactive(preferences, "task", new Date(2026, 0, 1, 12, 0)), true);
  const suggestionStarted = new Date(2026, 0, 1, 12, 0).getTime();
  assert.equal(shouldSpeakProactive(preferences, "suggestion", new Date(2026, 0, 1, 12, 30), suggestionStarted), true);
  assert.equal(shouldSpeakProactive(preferences, "suggestion", new Date(2026, 0, 1, 12, 10), suggestionStarted), false);
  assert.equal(shouldSpeakProactive({ ...preferences, doNotDisturb: true }, "reminder"), false);
  assert.throws(() => updateVoicePreference(preferences, "quietHoursStart", "25:00"), /Invalid value/);
  assert.match(completionSuggestion({ description: "Create website", output: "Created website folder." }), /review or open/);
  assert.equal(completionSuggestion({ description: "Reminder: stand up", output: "Completed." }), "");
});
