const test = require("node:test");
const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { PassThrough } = require("node:stream");
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

test("offline voice defaults to the multilingual base model and bounded CPU threads", async () => {
  const voiceDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "jarvis-voice-test-"));
  try {
    const runtime = new VoiceRuntime({ voiceDirectory });
    runtime.environment.JARVIS_WHISPER_THREADS = "";
    await runtime.initialize();
    assert.equal(runtime.modelPath, path.join(voiceDirectory, "ggml-base.bin"));
    assert.equal(runtime.whisperThreads, 4);
  } finally {
    await fs.rm(voiceDirectory, { recursive: true, force: true });
  }
});

test("offline voice rejects invalid thread limits", async () => {
  const runtime = new VoiceRuntime({ voiceDirectory: "unused" });
  runtime.environment.JARVIS_WHISPER_THREADS = "0";
  await assert.rejects(runtime.initialize(), /between 1 and 16/);
});

test("SAPI completion restores the listening state", async () => {
  const states = [];
  const runtime = new VoiceRuntime({
    voiceDirectory: "unused",
    onState: (state) => states.push(state),
    platform: "win32",
    spawnProcess: () => {
      const child = new EventEmitter();
      child.stderr = new PassThrough();
      child.kill = () => true;
      setImmediate(() => child.emit("close", 0));
      return child;
    },
  });
  runtime.environment.SystemRoot = "C:\\Windows";
  await runtime.speak("Hello.");
  assert.deepEqual(states, ["Speaking", "Listening"]);
});

test("offline transcription uses the configured CPU thread limit", async () => {
  let whisperArguments;
  const runtime = new VoiceRuntime({
    voiceDirectory: "unused",
    platform: "win32",
    spawnProcess: (_binary, args) => {
      whisperArguments = args;
      const child = new EventEmitter();
      child.stdout = new PassThrough();
      child.stderr = new PassThrough();
      setImmediate(() => {
        child.stdout.end("recognized speech\n");
        setImmediate(() => child.emit("close", 0));
      });
      return child;
    },
  });
  runtime.binaryPath = "whisper-cli.exe";
  runtime.modelPath = "ggml-base.bin";
  runtime.whisperThreads = 3;
  const transcript = await runtime.runWhisper("recording.wav", "auto");
  assert.equal(transcript, "recognized speech");
  assert.deepEqual(whisperArguments.slice(0, 7), [
    "-m", "ggml-base.bin", "-f", "recording.wav", "-l", "auto", "-t",
  ]);
  assert.equal(whisperArguments[7], "3");
});
