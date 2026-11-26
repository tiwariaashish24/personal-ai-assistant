const { spawn } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

const maxAudioBytes = 1024 * 1024;
const maxOutputBytes = 64 * 1024;
const maxQueuedSpeech = 16;

class VoiceRuntime {
  constructor({ voiceDirectory, onState = () => {}, spawnProcess = spawn, platform = process.platform }) {
    this.voiceDirectory = voiceDirectory;
    this.onState = onState;
    this.spawnProcess = spawnProcess;
    this.platform = platform;
    this.environment = { ...process.env };
    this.speechProcess = null;
    this.speechQueue = [];
    this.speechPump = null;
    this.interruptedSpeech = new WeakSet();
    this.binaryPath = "";
    this.modelPath = "";
    this.modelPaths = {};
    this.selectedModel = "base";
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
        const match = /^\s*(JARVIS_WHISPER_PATH|JARVIS_WHISPER_MODEL|JARVIS_WHISPER_BASE_MODEL|JARVIS_WHISPER_SMALL_MODEL|JARVIS_WHISPER_THREADS|JARVIS_WHISPER_HINGLISH_LANGUAGE|JARVIS_WHISPER_NO_SPEECH_THRESHOLD|JARVIS_WHISPER_TIMEOUT_SECONDS|JARVIS_SAPI_VOICE)\s*=\s*(.*?)\s*$/.exec(line);
        if (!match || this.environment[match[1]]) {
          continue;
        }
        this.environment[match[1]] = match[2].replace(/^(['"])(.*)\1$/, "$2");
      }
    }

    this.binaryPath = this.environment.JARVIS_WHISPER_PATH ||
      path.join(this.voiceDirectory, this.platform === "win32" ? "whisper-cli.exe" : "whisper-cli");
    const configuredModel = (this.environment.JARVIS_WHISPER_MODEL || "base").trim();
    const legacyModelPath = configuredModel === "base" || configuredModel === "small" ? "" : configuredModel;
    this.modelPaths = {
      base: this.environment.JARVIS_WHISPER_BASE_MODEL ||
        legacyModelPath || path.join(this.voiceDirectory, "ggml-base.bin"),
      small: this.environment.JARVIS_WHISPER_SMALL_MODEL ||
        path.join(this.voiceDirectory, "ggml-small.bin"),
    };
    this.selectedModel = configuredModel === "small" ? "small" : "base";
    this.modelPath = this.modelPaths[this.selectedModel];
    const configuredThreads = this.environment.JARVIS_WHISPER_THREADS || "4";
    if (!/^\d+$/.test(configuredThreads) || Number(configuredThreads) < 1 || Number(configuredThreads) > 16) {
      throw new Error("JARVIS_WHISPER_THREADS must be an integer between 1 and 16.");
    }
    this.whisperThreads = Number(configuredThreads);
    this.hinglishLanguage = this.environment.JARVIS_WHISPER_HINGLISH_LANGUAGE || "hi";
    if (!["auto", "en", "hi"].includes(this.hinglishLanguage)) {
      throw new Error("JARVIS_WHISPER_HINGLISH_LANGUAGE must be auto, en, or hi.");
    }
    this.noSpeechThreshold = this.environment.JARVIS_WHISPER_NO_SPEECH_THRESHOLD || "0.6";
    if (!/^(?:0(?:\.\d+)?|1(?:\.0+)?)$/.test(this.noSpeechThreshold)) {
      throw new Error("JARVIS_WHISPER_NO_SPEECH_THRESHOLD must be between 0 and 1.");
    }
    const configuredTimeout = this.environment.JARVIS_WHISPER_TIMEOUT_SECONDS || "90";
    if (!/^\d+$/.test(configuredTimeout) || Number(configuredTimeout) < 30 || Number(configuredTimeout) > 180) {
      throw new Error("JARVIS_WHISPER_TIMEOUT_SECONDS must be an integer between 30 and 180.");
    }
    this.transcriptionTimeoutMs = Number(configuredTimeout) * 1000;
    this.sapiVoice = this.environment.JARVIS_SAPI_VOICE || "";
    await fs.mkdir(this.voiceDirectory, { recursive: true });
  }

  async modelAvailability() {
    const models = {};
    for (const [name, filePath] of Object.entries(this.modelPaths)) {
      try {
        await fs.access(filePath);
        models[name] = true;
      } catch {
        models[name] = false;
      }
    }
    return models;
  }

  async selectModel(model) {
    if (model !== "base" && model !== "small") {
      throw new Error("Select the Whisper Base or Small model.");
    }
    try {
      await fs.access(this.modelPaths[model]);
    } catch {
      throw new Error(`Whisper ${model} is not installed. Run .\\install-whisper.ps1 -Model ${model}.`);
    }
    this.selectedModel = model;
    this.modelPath = this.modelPaths[model];
    return this.modelPath;
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
      throw new Error("Voice input must be a WAV recording under 1 MiB.");
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
        "-nth", this.noSpeechThreshold || "0.6",
        "-et", "2.4",
        "-lpt", "-1.0",
        "-sns",
        "-nt",
        "-np",
      ], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";
      let settled = false;
      const timeout = setTimeout(() => {
        child.kill();
        finish(new Error("Offline speech recognition timed out."));
      }, this.transcriptionTimeoutMs || 90000);
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
    if (this.speechQueue.length >= maxQueuedSpeech) {
      return Promise.reject(new Error("Speech queue is full; wait for current speech to finish."));
    }
    return new Promise((resolve, reject) => {
      this.speechQueue.push({ text: spokenText, resolve, reject });
      this.pumpSpeech();
    });
  }

  pumpSpeech() {
    if (this.speechPump) {
      return;
    }
    const run = async () => {
      while (this.speechQueue.length > 0) {
        const item = this.speechQueue.shift();
        try {
          await this.speakOne(item.text);
          if (this.speechQueue.length === 0) {
            this.onState("Listening");
          }
          item.resolve();
        } catch (error) {
          this.onState("Listening");
          item.reject(error);
          for (const queued of this.speechQueue.splice(0)) {
            queued.reject(new Error("Speech was stopped after an earlier playback failure."));
          }
          break;
        }
      }
    };
    const pumping = run().finally(() => {
      if (this.speechPump === pumping) {
        this.speechPump = null;
      }
      if (this.speechQueue.length > 0) {
        this.pumpSpeech();
      }
    });
    this.speechPump = pumping;
  }

  speakOne(spokenText) {
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
    return new Promise((resolve, reject) => {
      let stderr = "";
      let settled = false;
      let timeout;
      const finish = (error) => {
        if (settled) {
          return;
        }
        settled = true;
        clearTimeout(timeout);
        if (error) {
          reject(error);
        } else {
          resolve();
        }
      };
      const child = this.spawnProcess(powershell, [
        "-NoLogo", "-NoProfile", "-NonInteractive",
        "-EncodedCommand", encodedScript,
      ], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
      this.speechProcess = child;
      timeout = setTimeout(() => {
        this.interruptedSpeech.add(child);
        if (this.speechProcess === child) {
          this.speechProcess = null;
        }
        child.kill();
        finish(new Error("Windows SAPI speech timed out."));
      }, Math.max(30000, Math.min(180000, spokenText.length * 150)));
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk) => {
        stderr = (stderr + chunk).slice(-maxOutputBytes);
      });
      child.once("error", (error) => {
        this.speechProcess = null;
        if (this.interruptedSpeech.has(child)) {
          this.interruptedSpeech.delete(child);
          finish();
        } else {
          finish(new Error(`Could not start Windows SAPI speech: ${error.message}`));
        }
      });
      child.once("close", (code) => {
        if (this.speechProcess === child) {
          this.speechProcess = null;
        }
        if (code === 0 || this.interruptedSpeech.has(child)) {
          this.interruptedSpeech.delete(child);
          finish();
        } else {
          finish(new Error(`Windows SAPI speech failed${stderr.trim() ? `: ${stderr.trim()}` : ` (exit ${code})`}`));
        }
      });
    });
  }

  interrupt() {
    const child = this.speechProcess;
    const queued = this.speechQueue.splice(0);
    for (const item of queued) {
      item.resolve();
    }
    if (child) {
      this.interruptedSpeech.add(child);
      this.speechProcess = null;
      child.kill();
      this.onState("Listening");
    }
    return Boolean(child || queued.length);
  }

  close() {
    this.interrupt();
  }
}

module.exports = { VoiceRuntime };
