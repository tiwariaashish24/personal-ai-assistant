const test = require("node:test");
const assert = require("node:assert/strict");
const { VoiceRuntime } = require("./voice-runtime.cjs");

test("offline transcription rejects unsupported languages and malformed audio", async () => {
  const runtime = new VoiceRuntime({ voiceDirectory: "unused" });
  await assert.rejects(
    runtime.transcribe(Buffer.alloc(44), "fr"),
    /language must be auto, en, or hi/,
  );
  await assert.rejects(
    runtime.transcribe(Buffer.alloc(43), "auto"),
    /WAV recording under 512 KiB/,
  );
  assert.equal(runtime.interrupt(), false);
});
