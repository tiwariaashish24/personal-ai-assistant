const { spawn } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const http = require("node:http");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");

const maxAudioBytes = 1024 * 1024;
const maxOutputBytes = 64 * 1024;
const maxQueuedSpeech = 16;

function audioBufferFromIPC(wav) {
  if (Buffer.isBuffer(wav)) {
    return wav;
  }
  if (wav instanceof ArrayBuffer) {
    return Buffer.from(wav);
  }
  if (ArrayBuffer.isView(wav)) {
    return Buffer.from(wav.buffer, wav.byteOffset, wav.byteLength);
  }
  throw new Error("Voice input must arrive as an ArrayBuffer or byte array.");
}

function isMonoPcm16kWav(audio) {
  return audio.length >= 44 &&
    audio.toString("ascii", 0, 4) === "RIFF" &&
    audio.readUInt32LE(4) === audio.length - 8 &&
    audio.toString("ascii", 8, 12) === "WAVE" &&
    audio.toString("ascii", 12, 16) === "fmt " &&
    audio.readUInt32LE(16) === 16 &&
    audio.readUInt16LE(20) === 1 &&
    audio.readUInt16LE(22) === 1 &&
    audio.readUInt32LE(24) === 16000 &&
    audio.readUInt32LE(28) === 32000 &&
    audio.readUInt16LE(32) === 2 &&
    audio.readUInt16LE(34) === 16 &&
    audio.toString("ascii", 36, 40) === "data" &&
    audio.readUInt32LE(40) === audio.length - 44;
}

class VoiceRuntime {
  constructor({ voiceDirectory, onState = () => {}, spawnProcess = spawn, platform = process.platform, keepModelLoaded = false }) {
    this.voiceDirectory = voiceDirectory;
    this.onState = onState;
    this.spawnProcess = spawnProcess;
    this.platform = platform;
    this.keepModelLoaded = keepModelLoaded;
    this.whisperServer = null;
    this.whisperServerStart = null;
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
    this.whisperActive = 0;
    this.whisperLast = { status: "idle" };
    this.speechLast = { status: "idle" };
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

  async diagnostics() {
    return {
      ...(await this.availability()),
      executablePath: this.binaryPath,
      selectedModel: this.selectedModel,
      modelPath: this.modelPath,
      models: await this.modelAvailability(),
      whisper: {
        active: this.whisperActive > 0,
        activeCount: this.whisperActive,
        last: { ...this.whisperLast },
      },
      speech: {
        active: Boolean(this.speechProcess || this.speechPump),
        queued: this.speechQueue.length,
        last: { ...this.speechLast },
      },
    };
  }

  async transcribe(wav, language = "auto") {
    if (!["auto", "en", "hi"].includes(language)) {
      throw new Error("Offline speech language must be auto, en, or hi.");
    }
    const audio = audioBufferFromIPC(wav);
    if (audio.length > maxAudioBytes || !isMonoPcm16kWav(audio)) {
      throw new Error("Voice input must be a valid 16 kHz mono 16-bit PCM WAV recording under 1 MiB.");
    }
    const runtime = await this.availability();
    if (!runtime.available) {
      throw new Error(runtime.message);
    }
    const scratchDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "jarvis-voice-"));
    const audioPath = path.join(scratchDirectory, `${crypto.randomUUID()}.wav`);
    try {
      await fs.writeFile(audioPath, audio, { flag: "wx", mode: 0o600 });
      if (this.keepModelLoaded) {
        try {
          return await this.transcribeWithServer(audioPath, language);
        } catch (error) {
          // #region agent log
          try { require("node:fs").appendFileSync("e:\\USER\\OneDrive\\Desktop\\assistend\\.cursor\\debug-a054d2.log", `${JSON.stringify({ sessionId: "a054d2", runId: "pre-fix", hypothesisId: "B", location: "voice-runtime.cjs:transcribe", message: "warm whisper failed, using one-shot", data: { error: error.message }, timestamp: Date.now() })}\n`); } catch {}
          // #endregion
        }
      }
      return await this.runWhisper(audioPath, language);
    } finally {
      await fs.rm(scratchDirectory, { recursive: true, force: true });
    }
  }

  whisperServerBinary() {
    return path.join(path.dirname(this.binaryPath), this.platform === "win32" ? "whisper-server.exe" : "whisper-server");
  }

  prewarmWhisper() {
    if (!this.keepModelLoaded) {
      return Promise.resolve();
    }
    return this.ensureWhisperServer().catch(() => {});
  }

  async ensureWhisperServer() {
    if (this.whisperServer && this.whisperServer.modelPath === this.modelPath &&
        this.whisperServer.child.exitCode === null && !this.whisperServer.child.killed) {
      return this.whisperServer;
    }
    if (this.whisperServerStart) {
      return this.whisperServerStart;
    }
    this.whisperServerStart = this.startWhisperServer().finally(() => {
      this.whisperServerStart = null;
    });
    return this.whisperServerStart;
  }

  async startWhisperServer() {
    await this.stopWhisperServer();
    const serverBinary = this.whisperServerBinary();
    await fs.access(serverBinary);
    const port = 18766;
    const child = this.spawnProcess(serverBinary, [
      "-m", this.modelPath,
      "-l", "en",
      "-t", String(this.whisperThreads || 4),
      "-nt",
      "-sns",
      "-nth", this.noSpeechThreshold || "0.6",
      "-bs", "1",
      "-bo", "1",
      "-nf",
      "-nc",
      "--host", "127.0.0.1",
      "--port", String(port),
    ], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
    const server = { child, port, modelPath: this.modelPath };
    this.whisperServer = server;
    child.once("exit", () => {
      if (this.whisperServer === server) {
        this.whisperServer = null;
      }
    });
    try {
      await waitForPort(port, 20000);
    } catch (error) {
      child.kill();
      if (this.whisperServer === server) {
        this.whisperServer = null;
      }
      throw error;
    }
    return server;
  }

  async stopWhisperServer() {
    const server = this.whisperServer;
    this.whisperServer = null;
    if (!server) {
      return;
    }
    server.child.kill();
  }

  async transcribeWithServer(audioPath, language) {
    const startedAt = Date.now();
    const server = await this.ensureWhisperServer();
    const audio = await fs.readFile(audioPath);
    const prompt = language === "hi" ? "हे जार्विस।" : "Hey Jarvis.";
    const spokenLanguage = language === "auto" ? "en" : language;
    const body = multipartBody([
      ["response_format", "json"],
      ["temperature", "0"],
      ["language", spokenLanguage],
      ["prompt", prompt],
    ], audio);
    const responseText = await httpPost(
      server.port,
      "/inference",
      `multipart/form-data; boundary=${body.boundary}`,
      body.payload,
    );
    const parsed = JSON.parse(responseText);
    const transcript = String(parsed.text || "").replace(/\s+/g, " ").trim();
    // #region agent log
    try { require("node:fs").appendFileSync("e:\\USER\\OneDrive\\Desktop\\assistend\\.cursor\\debug-a054d2.log", `${JSON.stringify({ sessionId: "a054d2", runId: "pre-fix", hypothesisId: "B", location: "voice-runtime.cjs:transcribeWithServer", message: "warm whisper finished", data: { elapsedMs: Date.now() - startedAt, transcriptChars: transcript.length }, timestamp: Date.now() })}\n`); } catch {}
    // #endregion
    return transcript;
  }

  runWhisper(audioPath, language) {
    return new Promise((resolve, reject) => {
      const startedAt = Date.now();
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
        "--prompt", language === "hi" ? "हे जार्विस।" : "Hey Jarvis.",
        "-bs", "1",
        "-bo", "1",
        "-nf",
      ], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";
      let settled = false;
      this.whisperActive += 1;
      this.whisperLast = {
        status: "running",
        startedAt: new Date(startedAt).toISOString(),
        model: this.selectedModel,
        language,
      };
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
        this.whisperActive = Math.max(0, this.whisperActive - 1);
        this.whisperLast = {
          ...this.whisperLast,
          status: error ? "failed" : "completed",
          finishedAt: new Date().toISOString(),
          durationMs: Date.now() - startedAt,
          transcriptChars: typeof value === "string" ? value.length : 0,
          error: error ? error.message : "",
        };
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
      this.speechLast = { status: "queued", queuedAt: new Date().toISOString(), characters: spokenText.length };
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
    const startedAt = Date.now();
    this.speechLast = { status: "speaking", startedAt: new Date(startedAt).toISOString(), characters: spokenText.length };
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
        this.speechLast = {
          ...this.speechLast,
          status: this.interruptedSpeech.has(child) ? "interrupted" : error ? "failed" : "completed",
          finishedAt: new Date().toISOString(),
          durationMs: Date.now() - startedAt,
          error: error ? error.message : "",
        };
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
      this.speechLast = { ...this.speechLast, status: "interrupting", interruptedAt: new Date().toISOString() };
      child.kill();
      this.onState("Listening");
    }
    return Boolean(child || queued.length);
  }

  close() {
    this.interrupt();
    void this.stopWhisperServer();
  }
}

function waitForPort(port, timeoutMs) {
  const startedAt = Date.now();
  return new Promise((resolve, reject) => {
    const attempt = () => {
      const socket = net.connect(port, "127.0.0.1");
      socket.once("connect", () => {
        socket.end();
        resolve();
      });
      socket.once("error", () => {
        socket.destroy();
        if (Date.now() - startedAt > timeoutMs) {
          reject(new Error("The Whisper server did not open its port in time."));
          return;
        }
        setTimeout(attempt, 100);
      });
    };
    attempt();
  });
}

function multipartBody(fields, audio) {
  const boundary = `jarvis${crypto.randomUUID().replace(/-/g, "")}`;
  const chunks = [];
  for (const [name, value] of fields) {
    chunks.push(Buffer.from(
      `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`,
    ));
  }
  chunks.push(Buffer.from(
    `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="speech.wav"\r\nContent-Type: audio/wav\r\n\r\n`,
  ));
  chunks.push(audio);
  chunks.push(Buffer.from(`\r\n--${boundary}--\r\n`));
  return { boundary, payload: Buffer.concat(chunks) };
}

function httpPost(port, requestPath, contentType, payload) {
  return new Promise((resolve, reject) => {
    const request = http.request({
      hostname: "127.0.0.1",
      port,
      path: requestPath,
      method: "POST",
      headers: {
        "Content-Type": contentType,
        "Content-Length": payload.length,
      },
    }, (response) => {
      const chunks = [];
      response.on("data", (chunk) => chunks.push(chunk));
      response.on("end", () => {
        const text = Buffer.concat(chunks).toString("utf8");
        if (response.statusCode !== 200) {
          reject(new Error(`Whisper server returned HTTP ${response.statusCode}.`));
          return;
        }
        resolve(text);
      });
    });
    request.setTimeout(30000, () => {
      request.destroy(new Error("The warm Whisper request timed out."));
    });
    request.once("error", reject);
    request.end(payload);
  });
}

module.exports = { VoiceRuntime };
