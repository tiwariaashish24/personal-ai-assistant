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
  resolveConversationCommand,
  resemblesEcho,
  resampleTo16k,
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
  assert.equal(extractWakeCommand("Jarvis, open my calendar").activated, false);
  assert.equal(extractWakeCommand("Open my calendar").activated, false);
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
