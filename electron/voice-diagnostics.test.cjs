const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { appendVoiceDiagnostic } = require("./voice-diagnostics.cjs");

test("voice diagnostics persist bounded, sanitized errors without transcript fields", async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "jarvis-diagnostics-"));
  const logPath = path.join(directory, "voice-diagnostics.log");
  try {
    await appendVoiceDiagnostic(logPath, {
      stage: "transcription",
      message: "Whisper failed\nwith an error",
      transcript: "private speech",
    });
    const firstRecord = JSON.parse((await fs.readFile(logPath, "utf8")).trim());
    assert.equal(firstRecord.stage, "transcription");
    assert.equal(firstRecord.message, "Whisper failed with an error");
    assert.equal(Object.hasOwn(firstRecord, "transcript"), false);

    await fs.writeFile(logPath, "x".repeat(1024 * 1024 + 1));
    await appendVoiceDiagnostic(logPath, { stage: "audio-context", message: "resume failed" });
    const boundedLog = await fs.readFile(logPath, "utf8");
    assert.ok(Buffer.byteLength(boundedLog, "utf8") < 1024 * 1024);
    assert.equal(JSON.parse(boundedLog.trim()).stage, "audio-context");
    await assert.rejects(
      appendVoiceDiagnostic(logPath, { stage: "Invalid Stage", message: "error" }),
      /Invalid voice diagnostic record/,
    );
  } finally {
    await fs.rm(directory, { recursive: true, force: true });
  }
});
