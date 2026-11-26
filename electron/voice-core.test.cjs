const test = require("node:test");
const assert = require("node:assert/strict");
const {
  VoiceActivitySegmenter,
  VoiceOnsetDetector,
  encodeWav,
  extractWakeCommand,
  resolveConversationCommand,
  resemblesEcho,
  resampleTo16k,
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
