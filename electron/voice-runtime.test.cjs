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
    /WAV recording under 1 MiB/,
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
    assert.equal(runtime.selectedModel, "base");
    assert.equal(runtime.whisperThreads, 4);
  } finally {
    await fs.rm(voiceDirectory, { recursive: true, force: true });
  }
});

test("offline voice selects either installed multilingual Whisper model", async () => {
  const voiceDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "jarvis-model-test-"));
  try {
    const basePath = path.join(voiceDirectory, "ggml-base.bin");
    const smallPath = path.join(voiceDirectory, "ggml-small.bin");
    await fs.writeFile(basePath, "base");
    await fs.writeFile(smallPath, "small");
    const runtime = new VoiceRuntime({ voiceDirectory });
    await runtime.initialize();
    assert.deepEqual(await runtime.modelAvailability(), { base: true, small: true });
    await runtime.selectModel("small");
    assert.equal(runtime.modelPath, smallPath);
    await assert.rejects(runtime.selectModel("tiny"), /Base or Small/);
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
  assert.deepEqual(whisperArguments.slice(8, 15), [
    "-nth", "0.6", "-et", "2.4", "-lpt", "-1.0", "-sns",
  ]);
});

test("offline transcription terminates and reports a hung Whisper process", async () => {
  let killed = false;
  const runtime = new VoiceRuntime({
    voiceDirectory: "unused",
    platform: "win32",
    spawnProcess: () => {
      const child = new EventEmitter();
      child.stdout = new PassThrough();
      child.stderr = new PassThrough();
      child.kill = () => {
        killed = true;
        return true;
      };
      return child;
    },
  });
  runtime.binaryPath = "whisper-cli.exe";
  runtime.modelPath = "ggml-base.bin";
  runtime.transcriptionTimeoutMs = 5;
  await assert.rejects(runtime.runWhisper("recording.wav", "en"), /timed out/);
  assert.equal(killed, true);
});

test("SAPI speech queue is serialized and interruption clears queued playback", async () => {
  let spawnCount = 0;
  let activeChild;
  const runtime = new VoiceRuntime({
    voiceDirectory: "unused",
    platform: "win32",
    spawnProcess: () => {
      spawnCount += 1;
      const child = new EventEmitter();
      child.stderr = new PassThrough();
      child.kill = () => {
        setImmediate(() => child.emit("close", 1));
        return true;
      };
      activeChild = child;
      return child;
    },
  });
  runtime.environment.SystemRoot = "C:\\Windows";
  const first = runtime.speak("First sentence.");
  const queued = runtime.speak("Queued sentence.");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(spawnCount, 1);
  assert.equal(activeChild !== null, true);
  assert.equal(runtime.interrupt(), true);
  await Promise.all([first, queued]);
  assert.equal(spawnCount, 1);
  assert.equal(runtime.interrupt(), false);
});

test("SAPI speech queue rejects additional pending speech at its bounded capacity", async () => {
  let activeChild;
  const runtime = new VoiceRuntime({
    voiceDirectory: "unused",
    platform: "win32",
    spawnProcess: () => {
      const child = new EventEmitter();
      child.stderr = new PassThrough();
      child.kill = () => {
        setImmediate(() => child.emit("close", 1));
        return true;
      };
      activeChild = child;
      return child;
    },
  });
  runtime.environment.SystemRoot = "C:\\Windows";
  const pending = [runtime.speak("Active.")];
  for (let index = 0; index < 16; index += 1) {
    pending.push(runtime.speak(`Queued ${index}.`));
  }
  await assert.rejects(runtime.speak("Overflow."), /Speech queue is full/);
  await new Promise((resolve) => setImmediate(resolve));
  runtime.interrupt();
  await Promise.all(pending);
  assert.ok(activeChild);
});
