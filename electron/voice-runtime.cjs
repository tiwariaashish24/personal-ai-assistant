const { spawn } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

const maxAudioBytes = 512 * 1024;
const maxOutputBytes = 64 * 1024;

class VoiceRuntime {
  constructor({ voiceDirectory, onState = () => {}, spawnProcess = spawn, platform = process.platform }) {
    this.voiceDirectory = voiceDirectory;
    this.onState = onState;
    this.spawnProcess = spawnProcess;
    this.platform = platform;
    this.environment = { ...process.env };
    this.speechProcess = null;
    this.speechPromise = null;
    this.interruptedSpeech = new WeakSet();
    this.binaryPath = "";
    this.modelPath = "";
    this.sapiVoice = "";
  }

  async initialize(dotEnvPaths = []) {
    for (const dotenvPath of dotEnvPaths) {
      let contents;
      try {
        contents = await fs.readFile(dotenvPath, "utf8");
      } catch (error) {
        if (error.code === "ENOENT") {
          continue;
        }
        throw new Error(`Could not read voice configuration: ${error.message}`);
      }
      for (const line of contents.split(/\r?\n/)) {
        const match = /^\s*(JARVIS_WHISPER_PATH|JARVIS_WHISPER_MODEL|JARVIS_WHISPER_THREADS|JARVIS_SAPI_VOICE)\s*=\s*(.*?)\s*$/.exec(line);
        if (!match || this.environment[match[1]]) {
          continue;
        }
        this.environment[match[1]] = match[2].replace(/^(['"])(.*)\1$/, "$2");
      }
    }

    this.binaryPath = this.environment.JARVIS_WHISPER_PATH ||
      path.join(this.voiceDirectory, process.platform === "win32" ? "whisper-cli.exe" : "whisper-cli");
    this.modelPath = this.environment.JARVIS_WHISPER_MODEL ||
      path.join(this.voiceDirectory, "ggml-base.bin");
    const configuredThreads = this.environment.JARVIS_WHISPER_THREADS || "4";
    if (!/^\d+$/.test(configuredThreads) || Number(configuredThreads) < 1 || Number(configuredThreads) > 16) {
      throw new Error("JARVIS_WHISPER_THREADS must be an integer between 1 and 16.");
    }
    this.whisperThreads = Number(configuredThreads);
    this.sapiVoice = this.environment.JARVIS_SAPI_VOICE || "";
    await fs.mkdir(this.voiceDirectory, { recursive: true });
  }

  async availability() {
    if (this.platform !== "win32") {
      return { available: false, message: "The configured offline voice runtime currently targets Windows." };
    }
    for (const filePath of [this.binaryPath, this.modelPath]) {
      try {
        await fs.access(filePath);
      } catch {
        return {
          available: false,
          message: "Install whisper.cpp and a multilingual Whisper model; see electron/README.md.",
        };
      }
    }
    return { available: true, message: "Offline Whisper and Windows SAPI are ready." };
  }

  async transcribe(wav, language = "auto") {
    if (!["auto", "en", "hi"].includes(language)) {
      throw new Error("Offline speech language must be auto, en, or hi.");
    }
    const audio = Buffer.isBuffer(wav) ? wav : Buffer.from(wav);
    if (audio.length < 44 || audio.length > maxAudioBytes ||
        audio.toString("ascii", 0, 4) !== "RIFF" || audio.toString("ascii", 8, 12) !== "WAVE") {
      throw new Error("Voice input must be a WAV recording under 512 KiB.");
    }
    const runtime = await this.availability();
    if (!runtime.available) {
      throw new Error(runtime.message);
    }
    const scratchDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "jarvis-voice-"));
    const audioPath = path.join(scratchDirectory, `${crypto.randomUUID()}.wav`);
    try {
      await fs.writeFile(audioPath, audio, { flag: "wx", mode: 0o600 });
      return await this.runWhisper(audioPath, language);
    } finally {
      await fs.rm(scratchDirectory, { recursive: true, force: true });
    }
  }

  runWhisper(audioPath, language) {
    return new Promise((resolve, reject) => {
      const child = this.spawnProcess(this.binaryPath, [
        "-m", this.modelPath,
        "-f", audioPath,
        "-l", language,
        "-t", String(this.whisperThreads),
        "-nt",
        "-np",
      ], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";
      let settled = false;
      const timeout = setTimeout(() => {
        child.kill();
        finish(new Error("Offline speech recognition timed out."));
      }, 45000);
      const finish = (error, value) => {
        if (settled) {
          return;
        }
        settled = true;
        clearTimeout(timeout);
        if (error) {
          reject(error);
        } else {
          resolve(value);
        }
      };
      child.stdout.setEncoding("utf8");
      child.stderr.setEncoding("utf8");
      child.stdout.on("data", (chunk) => {
        stdout = (stdout + chunk).slice(-maxOutputBytes);
      });
      child.stderr.on("data", (chunk) => {
        stderr = (stderr + chunk).slice(-maxOutputBytes);
      });
      child.once("error", (error) => finish(new Error(`Could not start whisper.cpp: ${error.message}`)));
      child.once("close", (code) => {
        if (code !== 0) {
          finish(new Error(`whisper.cpp failed${stderr.trim() ? `: ${stderr.trim()}` : ` (exit ${code})`}`));
          return;
        }
        const transcript = stdout
          .split(/\r?\n/)
          .map((line) => line.replace(/^\s*\[[^\]]+\]\s*/, "").trim())
          .filter(Boolean)
          .join(" ")
          .trim();
        finish(null, transcript);
      });
    });
  }

  speak(text) {
    if (this.platform !== "win32") {
      return Promise.reject(new Error("Offline SAPI speech output is available on Windows only."));
    }
    const spokenText = String(text || "").trim();
    if (!spokenText) {
      return Promise.resolve();
    }
    if (spokenText.length > 4000) {
      return Promise.reject(new Error("Speech output exceeds the 4,000 character limit."));
    }
    if (this.speechPromise) {
      return this.speechPromise.then(() => this.speak(spokenText));
    }

    const textBase64 = Buffer.from(spokenText, "utf16le").toString("base64");
    const voiceBase64 = Buffer.from(this.sapiVoice, "utf16le").toString("base64");
    const script = [
      "$ErrorActionPreference = 'Stop'",
      "Add-Type -AssemblyName System.Speech",
      "$text = [System.Text.Encoding]::Unicode.GetString([System.Convert]::FromBase64String('" + textBase64 + "'))",
      "$voice = [System.Text.Encoding]::Unicode.GetString([System.Convert]::FromBase64String('" + voiceBase64 + "'))",
      "$s = New-Object System.Speech.Synthesis.SpeechSynthesizer",
      "if ($voice) { $s.SelectVoice($voice) } else {",
      "  $culture = if ($text -match '[\\u0900-\\u097F]') { 'hi' } else { 'en' }",
      "  $installed = $s.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name.StartsWith($culture) } | Select-Object -First 1",
      "  if ($installed) { $s.SelectVoice($installed.VoiceInfo.Name) }",
      "}",
      "$s.Speak($text)",
      "$s.Dispose()",
    ].join("; ");
    const encodedScript = Buffer.from(script, "utf16le").toString("base64");
    const powershell = path.join(
      this.environment.SystemRoot || "C:\\Windows",
      "System32",
      "WindowsPowerShell",
      "v1.0",
      "powershell.exe",
    );

    this.onState("Speaking");
    this.speechPromise = new Promise((resolve, reject) => {
      let stderr = "";
      const child = this.spawnProcess(powershell, [
        "-NoLogo", "-NoProfile", "-NonInteractive",
        "-EncodedCommand", encodedScript,
      ], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
      this.speechProcess = child;
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk) => {
        stderr = (stderr + chunk).slice(-maxOutputBytes);
      });
      child.once("error", (error) => {
        this.speechProcess = null;
        if (this.interruptedSpeech.has(child)) {
          resolve();
        } else {
          reject(new Error(`Could not start Windows SAPI speech: ${error.message}`));
        }
      });
      child.once("close", (code) => {
        if (this.speechProcess === child) {
          this.speechProcess = null;
        }
        if (code === 0 || this.interruptedSpeech.has(child)) {
          this.interruptedSpeech.delete(child);
          resolve();
        } else {
          reject(new Error(`Windows SAPI speech failed${stderr.trim() ? `: ${stderr.trim()}` : ` (exit ${code})`}`));
        }
      });
    }).finally(() => {
      this.speechPromise = null;
      this.onState("Listening");
    });
    return this.speechPromise;
  }

  interrupt() {
    const child = this.speechProcess;
    if (!child) {
      return false;
    }
    this.interruptedSpeech.add(child);
    this.speechProcess = null;
    child.kill();
    this.onState("Listening");
    return true;
  }

  close() {
    this.interrupt();
  }
}

module.exports = { VoiceRuntime };
