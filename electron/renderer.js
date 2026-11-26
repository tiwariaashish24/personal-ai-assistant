const voiceCore = window.JarvisVoiceCore;

// #region agent log
function agentDebugLog(hypothesisId, location, message, data) {
  fetch("http://127.0.0.1:7298/ingest/5c6b63f7-1d20-47e6-a655-0bc59455c0ef", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Debug-Session-Id": "a054d2" },
    body: JSON.stringify({ sessionId: "a054d2", runId: "pre-fix", hypothesisId, location, message, data, timestamp: Date.now() }),
  }).catch(() => {});
}
// #endregion
const microphoneButton = document.querySelector("#microphone-button");
const microphoneLabel = document.querySelector("#microphone-label");
const muteButton = document.querySelector("#mute-button");
const languagePicker = document.querySelector("#language");
const microphonePicker = document.querySelector("#microphone-device");
const whisperModelPicker = document.querySelector("#whisper-model");
const inputLevelMeter = document.querySelector("#input-level-meter");
const inputLevelLabel = document.querySelector("#input-level-label");
const statusText = document.querySelector("#status");
const voiceDetail = document.querySelector("#voice-detail");
const conversation = document.querySelector("#conversation");
const commandForm = document.querySelector("#command-form");
const commandInput = document.querySelector("#command-input");
const workspacePath = document.querySelector("#workspace-path");
const microphonePrivacy = document.querySelector("#microphone-privacy");
const microphoneIndicator = document.querySelector("#microphone-indicator");
const integrationsList = document.querySelector("#integrations-list");
const backgroundTasks = document.querySelector("#background-tasks");
const diagnosticsOutput = document.querySelector("#diagnostics-output");
const diagnosticsButton = document.querySelector("#diagnostics-button");
const latencyOutput = document.querySelector("#latency-output");
const pipelineOutput = document.querySelector("#voice-pipeline-status");
const rawTranscriptOutput = document.querySelector("#raw-transcript");

let voiceSettings;
let voiceState = "Offline";
let listening = false;
let muted = false;
let activeConversation = false;
let conversationTimer;
let mediaStream;
let audioContext;
let audioNode;
let audioSource;
let segmenter;
let bargeInDetector;
let bargeInRequested = false;
let processing = false;
let streamingMessage;
let transcriptionQueue = Promise.resolve();
let commandQueue = Promise.resolve();
let currentSpokenText = "";
let spokenEchoWindowUntil = 0;
let activeSpeechQueue;
let lastAcceptedTranscript = "";
let lastAcceptedTranscriptAt = 0;
let lastSuggestionAt = 0;
let inputLevelUpdatedAt = 0;
let activeCommandStartedAt = 0;
const latencyMetrics = {
  transcriptionMs: null,
  firstAiTokenMs: null,
  firstSpeechMs: null,
};
let knownTaskStates = new Map();
let taskPollTimer;
let taskBaselineLoaded = false;
let taskPollPending = false;
const rendererStartedAt = Date.now();
const announcedTaskIds = new Set();
let captureGeneration = 0;
let captureStartPromise;
let cancelPendingFrameCheck;
let audioContextResumePending = false;
let audioFramesReceived = 0;
let capturedSamples = 0;
let lastPipelineRenderAt = 0;
let rawTranscriptTimeout;
const pipelineDiagnostics = {
  microphone: "starting",
  audioContext: "not initialized",
  audioWorklet: "waiting for frames",
  capturedAudio: "0.00 seconds",
  vad: "waiting for speech",
  lastUtterance: "none",
  transcription: "idle",
  wakeWord: "waiting for Hey Jarvis",
  session: "inactive; wake phrase required",
  backend: "not tested",
  ai: "idle",
  speech: "idle",
  whisper: "not checked",
  diagnosticLog: "saved locally on errors",
};

function renderPipelineDiagnostics() {
  const vadSettings = segmenter
    ? `RMS ${segmenter.threshold.toFixed(3)}; min ${Math.round(segmenter.minimumSamples * 1000 / segmenter.sampleRate)} ms; silence ${Math.round(segmenter.silenceSamples * 1000 / segmenter.sampleRate)} ms`
    : "RMS 0.012; min 180 ms; silence 700 ms";
  pipelineOutput.textContent = [
    `Microphone: ${pipelineDiagnostics.microphone}`,
    `AudioContext: ${pipelineDiagnostics.audioContext}`,
    `AudioWorklet: ${pipelineDiagnostics.audioWorklet}`,
    `Captured audio: ${pipelineDiagnostics.capturedAudio}`,
    `VAD: ${pipelineDiagnostics.vad} (${vadSettings})`,
    `Last utterance: ${pipelineDiagnostics.lastUtterance}`,
    `Whisper: ${pipelineDiagnostics.whisper}`,
    `Transcription: ${pipelineDiagnostics.transcription}`,
    `Wake word: ${pipelineDiagnostics.wakeWord}`,
    `Conversation: ${pipelineDiagnostics.session}`,
    `Go backend: ${pipelineDiagnostics.backend}`,
    `AI assistant (Ollama): ${pipelineDiagnostics.ai}`,
    `Windows SAPI: ${pipelineDiagnostics.speech}`,
    `Persistent errors: ${pipelineDiagnostics.diagnosticLog}`,
  ].join("\n");
}

function updatePipelineDiagnostic(stage, status) {
  if (pipelineDiagnostics[stage] === status) {
    return;
  }
  pipelineDiagnostics[stage] = status;
  renderPipelineDiagnostics();
}

function showRawTranscript(transcript) {
  rawTranscriptOutput.textContent = transcript || "(empty transcript)";
  window.clearTimeout(rawTranscriptTimeout);
  rawTranscriptTimeout = window.setTimeout(() => {
    rawTranscriptOutput.textContent = "Transcript cleared.";
  }, 15000);
}

function reportVoiceError(stage, error) {
  const message = error instanceof Error ? error.message : String(error);
  updatePipelineDiagnostic(stage, `ERROR: ${message}`);
  setVoiceState("Error", message);
  const logStage = stage.replace(/[A-Z]/gu, (letter) => `-${letter.toLowerCase()}`);
  void window.jarvis.logVoiceDiagnostic({ stage: logStage, message }).catch((logError) => {
    console.error(`Could not persist ${stage} diagnostic: ${logError.message}`);
  });
}

function setStatus(message) {
  voiceDetail.textContent = message;
}

function setVoiceState(state, detail = "") {
  voiceState = state;
  statusText.textContent = state;
  voiceDetail.textContent = detail;
  document.body.dataset.voiceState = state.toLowerCase().replaceAll(" ", "-");
  document.body.classList.toggle("is-listening", listening);
  microphoneIndicator.textContent = muted
    ? "Microphone is muted"
    : listening
      ? "Microphone is on; audio is processed locally"
      : "Microphone is off";
  microphonePrivacy.textContent = muted
    ? "Microphone is muted."
    : listening
      ? "Local wake-word and speech recognition are active; no recordings are retained."
      : "Listening is paused; the microphone is off.";
  updateMicrophoneButtons();
  window.jarvis.setVoiceState(state, detail);
}

function addMessage(role, text) {
  conversation.querySelector(".empty-state")?.remove();
  const message = document.createElement("div");
  message.className = `message ${role}`;
  message.textContent = text;
  conversation.append(message);
  conversation.scrollTop = conversation.scrollHeight;
}

function appendAssistantDelta(delta) {
  if (typeof delta !== "string" || delta.length === 0) {
    return;
  }
  if (!streamingMessage) {
    conversation.querySelector(".empty-state")?.remove();
    streamingMessage = document.createElement("div");
    streamingMessage.className = "message assistant streaming";
    conversation.append(streamingMessage);
  }
  streamingMessage.textContent += delta;
  conversation.scrollTop = conversation.scrollHeight;
}

function finishAssistantStream(text) {
  if (streamingMessage) {
    streamingMessage.textContent = text;
    streamingMessage.classList.remove("streaming");
    streamingMessage = undefined;
  } else {
    addMessage("assistant", text);
  }
}

function discardAssistantStream() {
  if (streamingMessage) {
    streamingMessage.remove();
    streamingMessage = undefined;
  }
}

async function speak(text) {
  if (!text) {
    return;
  }
  updatePipelineDiagnostic("speech", "requesting Windows SAPI playback");
  bargeInDetector?.reset();
  bargeInRequested = false;
  currentSpokenText = text;
  spokenEchoWindowUntil = Number.POSITIVE_INFINITY;
  setVoiceState("Speaking");
  try {
    await window.jarvis.speak(text);
    updatePipelineDiagnostic("speech", "playback completed");
  } catch (error) {
    reportVoiceError("speech", error);
    throw error;
  } finally {
    spokenEchoWindowUntil = Date.now() + 5000;
  }
}

async function interruptCurrentSpeech() {
  updatePipelineDiagnostic("speech", "cancelled by voice interruption");
  currentSpokenText = "";
  spokenEchoWindowUntil = 0;
  if (activeSpeechQueue) {
    await activeSpeechQueue.cancel();
    return;
  }
  await window.jarvis.interruptSpeech();
}

function updateMicrophoneButtons() {
  microphoneLabel.textContent = voiceSettings?.listeningEnabled
    ? voiceState === "Error" && !listening ? "Retry microphone" : "Pause listening"
    : "Resume listening";
  muteButton.textContent = muted ? "Unmute microphone" : "Mute microphone";
  microphoneButton.disabled = false;
}

function transcriptionLanguage() {
  if (languagePicker.value === "en-US") {
    return "en";
  }
  if (languagePicker.value === "hi-IN") {
    return "hi";
  }
  return voiceSettings?.hinglishLanguage || "hi";
}

function updateInputLevel(samples) {
  const { rms, decibels } = voiceCore.analyzeAudio(samples);
  inputLevelMeter.value = Math.min(1, rms * 8);
  inputLevelLabel.textContent = Number.isFinite(decibels) ? `${Math.round(decibels)} dBFS` : "-inf dBFS";
}

async function refreshMicrophoneDevices() {
  if (!navigator.mediaDevices?.enumerateDevices) {
    microphonePicker.disabled = true;
    microphonePicker.replaceChildren(new Option("Microphone selection unavailable", ""));
    return;
  }
  const devices = (await navigator.mediaDevices.enumerateDevices())
    .filter((device) => device.kind === "audioinput");
  microphonePicker.replaceChildren(new Option("System default", ""));
  devices.forEach((device, index) => {
    const option = new Option(device.label || `Microphone ${index + 1}`, device.deviceId);
    microphonePicker.append(option);
  });
  const selected = voiceSettings?.microphoneDeviceId || "";
  if (selected && !devices.some((device) => device.deviceId === selected)) {
    const unavailable = new Option("Saved microphone unavailable", selected);
    unavailable.disabled = true;
    microphonePicker.append(unavailable);
  }
  microphonePicker.value = selected;
  microphonePicker.disabled = false;
}

async function saveVoiceSetting(key, value) {
  const updated = await window.jarvis.setVoiceSetting(key, value);
  voiceSettings = { ...voiceSettings, ...updated };
  return updated;
}

function renderLatencyMetrics() {
  const format = (value) => value === null ? "not measured" : `${value} ms`;
  latencyOutput.textContent = [
    `Whisper transcription: ${format(latencyMetrics.transcriptionMs)}`,
    `First AI text token: ${format(latencyMetrics.firstAiTokenMs)}`,
    `First SAPI speech start: ${format(latencyMetrics.firstSpeechMs)}`,
  ].join("\n");
}

function renderVoiceSettings() {
  const models = voiceSettings?.whisperModels || {};
  for (const option of whisperModelPicker.options) {
    const installed = models[option.value];
    option.disabled = installed === false;
    if (option.value === "base") {
      option.textContent = installed === false ? "Whisper Base (not installed)" : "Whisper Base";
    } else if (option.value === "small") {
      option.textContent = installed === false ? "Whisper Small (not installed)" : "Whisper Small";
    }
  }
  whisperModelPicker.value = voiceSettings?.whisperModel || "base";
  document.querySelector("#proactive-enabled").checked = voiceSettings?.proactiveSuggestionsEnabled !== false;
  document.querySelector("#dnd-enabled").checked = voiceSettings?.doNotDisturb === true;
  document.querySelector("#task-announcements").checked = voiceSettings?.announceTaskCompletions !== false;
  document.querySelector("#reminder-announcements").checked = voiceSettings?.announceReminders !== false;
  document.querySelector("#quiet-hours-start").value = voiceSettings?.quietHoursStart || "22:00";
  document.querySelector("#quiet-hours-end").value = voiceSettings?.quietHoursEnd || "08:00";
  document.querySelector("#proactive-cooldown").value = String(voiceSettings?.proactiveCooldownMinutes || 30);
}

function updateWhisperDiagnostics(runtime = voiceSettings?.runtime || {}) {
  updatePipelineDiagnostic(
    "whisper",
    `${runtime.available ? "ready" : runtime.message || "unavailable"}; model=${runtime.selectedModel || voiceSettings?.whisperModel || "unknown"}; executable=${runtime.executablePath || "unknown"}; model path=${runtime.modelPath || "unknown"}`,
  );
}

async function persistVoiceSetting(key, value) {
  try {
    return await saveVoiceSetting(key, value);
  } catch (error) {
    setVoiceState("Error", `Could not save ${key}: ${error.message}`);
    throw error;
  }
}

async function runPerformanceDiagnostics() {
  diagnosticsButton.disabled = true;
  diagnosticsOutput.textContent = "Measuring system usage…";
  try {
    const metrics = await window.jarvis.getDiagnostics();
    const gpu = metrics.gpu.available
      ? `${metrics.gpu.name}: ${metrics.gpu.usedMiB}/${metrics.gpu.totalMiB} MiB VRAM, ${metrics.gpu.utilizationPercent}%`
      : metrics.gpu.error;
    const processes = metrics.appProcesses
      .map((process) => `${process.type} PID ${process.pid}: ${process.cpuPercent}% CPU, ${process.workingSetMiB} MiB`)
      .join("\n");
    diagnosticsOutput.textContent = [
      `Measured: ${metrics.measuredAt}`,
      `System CPU: ${metrics.systemCpuPercent}%`,
      `JARVIS main-process CPU: ${metrics.mainProcessCpuPercent}%`,
      `System RAM: ${metrics.systemMemoryMiB.used}/${metrics.systemMemoryMiB.total} MiB used`,
      `GPU: ${gpu}`,
      "Electron processes:",
      processes || "No Electron process metrics available.",
    ].join("\n");
  } catch (error) {
    diagnosticsOutput.textContent = `Diagnostics failed: ${error.message}`;
  } finally {
    diagnosticsButton.disabled = false;
  }
}

async function stopCapture(nextState = "Sleep", detail = "") {
  captureGeneration += 1;
  listening = false;
  cancelPendingFrameCheck?.();
  cancelPendingFrameCheck = undefined;
  activeConversation = false;
  updateSessionDiagnostic();
  window.clearTimeout(conversationTimer);
  if (audioNode) {
    audioNode.port.onmessage = null;
    audioNode.disconnect();
    audioNode = undefined;
  }
  if (audioSource) {
    audioSource.disconnect();
    audioSource = undefined;
  }
  if (mediaStream) {
    for (const track of mediaStream.getTracks()) {
      track.stop();
    }
    mediaStream = undefined;
  }
  if (audioContext) {
    const context = audioContext;
    audioContext = undefined;
    await context.close();
  }
  bargeInDetector = undefined;
  bargeInRequested = false;
  updatePipelineDiagnostic("microphone", "off");
  updatePipelineDiagnostic("audioContext", "closed");
  updatePipelineDiagnostic("audioWorklet", "stopped");
  updatePipelineDiagnostic("vad", "paused");
  inputLevelMeter.value = 0;
  inputLevelLabel.textContent = "-inf dBFS";
  document.body.classList.remove("is-listening", "is-awake");
  updateMicrophoneButtons();
  setVoiceState(nextState, detail);
}

async function resumeActiveAudioContext(context) {
  if (audioContextResumePending) {
    return false;
  }
  audioContextResumePending = true;
  try {
    const sampleRate = await voiceCore.resumeAudioContext(context);
    updatePipelineDiagnostic("audioContext", `running at ${sampleRate} Hz; Whisper input is 16000 Hz`);
    return true;
  } catch (error) {
    await stopCapture("Error", `Microphone AudioContext could not resume: ${error.message}`);
    reportVoiceError("audioContext", error);
    return false;
  } finally {
    audioContextResumePending = false;
  }
}

async function startCapture() {
  if (listening) {
    if (audioContext && audioContext.state !== "running") {
      await resumeActiveAudioContext(audioContext);
    }
    return;
  }
  if (captureStartPromise) {
    return captureStartPromise;
  }
  if (muted || !voiceSettings?.listeningEnabled) {
    // #region agent log
    agentDebugLog("E", "renderer.js:startCapture", "capture blocked before microphone start", {
      muted,
      listeningEnabled: Boolean(voiceSettings?.listeningEnabled),
      voiceState,
    });
    // #endregion
    await stopCapture("Sleep", muted ? "Microphone is muted." : "Listening is paused.");
    return;
  }
  if (!voiceSettings?.runtime?.available) {
    // #region agent log
    agentDebugLog("B", "renderer.js:startCapture", "offline voice runtime unavailable", {
      message: voiceSettings?.runtime?.message || "",
      whisperModel: voiceSettings?.whisperModel || "",
    });
    // #endregion
    updatePipelineDiagnostic("whisper", voiceSettings?.runtime?.message || "Offline runtime unavailable");
    await stopCapture("Offline", voiceSettings?.runtime?.message || "Configure offline voice recognition.");
    return;
  }
  const generation = ++captureGeneration;
  captureStartPromise = (async () => {
    let failed = false;
    let failureStage = "microphone";
    let frameTimeout;
    try {
      audioFramesReceived = 0;
      capturedSamples = 0;
      updatePipelineDiagnostic("microphone", "requesting access");
      updatePipelineDiagnostic("audioWorklet", "loading");
      updatePipelineDiagnostic("vad", "waiting for speech");
      updatePipelineDiagnostic("transcription", "idle");
      updatePipelineDiagnostic("wakeWord", "waiting for Hey Jarvis");
      failureStage = "audioContext";
      try {
        audioContext = new AudioContext({ sampleRate: voiceCore.TARGET_SAMPLE_RATE });
      } catch {
        audioContext = new AudioContext();
        updatePipelineDiagnostic("audioContext", `16 kHz unavailable; will resample from ${audioContext.sampleRate} Hz`);
      }
      const context = audioContext;
      context.addEventListener("statechange", () => {
        updatePipelineDiagnostic(
          "audioContext",
          `${context.state} at ${context.sampleRate} Hz; Whisper input is 16000 Hz`,
        );
        if (context.state === "suspended" && listening && audioContext === context) {
          void resumeActiveAudioContext(context);
        }
      });
      const contextReady = voiceCore.resumeAudioContext(context)
        .then((sampleRate) => ({ sampleRate }), (error) => ({ error }));
      failureStage = "audioWorklet";
      const workletReady = context.audioWorklet.addModule("voice-capture-worklet.js")
        .then(() => ({}), (error) => ({ error }));
      updatePipelineDiagnostic("audioContext", `resuming at ${context.sampleRate} Hz`);
      const audioConstraints = {
        channelCount: 1,
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
        ...(voiceSettings.microphoneDeviceId ? { deviceId: { exact: voiceSettings.microphoneDeviceId } } : {}),
      };
      failureStage = "microphone";
      const stream = await navigator.mediaDevices.getUserMedia({ audio: audioConstraints });
      if (generation !== captureGeneration || muted || !voiceSettings?.listeningEnabled) {
        for (const track of stream.getTracks()) {
          track.stop();
        }
        return;
      }
      mediaStream = stream;
      const microphoneTrack = mediaStream.getAudioTracks()[0];
      const inputSampleRate = microphoneTrack?.getSettings?.().sampleRate;
      const inputName = microphoneTrack?.label || "default input";
      updatePipelineDiagnostic(
        "microphone",
        `access granted: ${inputName}${inputSampleRate ? ` (${inputSampleRate} Hz)` : ""}`,
      );
      microphoneTrack?.addEventListener("mute", () => {
        updatePipelineDiagnostic("microphone", "input track is muted");
      });
      microphoneTrack?.addEventListener("unmute", () => {
        updatePipelineDiagnostic("microphone", microphoneTrack.label || "input track active");
      });
      microphoneTrack?.addEventListener("ended", () => {
        if (generation === captureGeneration) {
          reportVoiceError("microphone", new Error("The selected microphone track ended."));
        }
      });
      void refreshMicrophoneDevices().catch((error) => {
        microphonePicker.title = `Could not enumerate microphone devices: ${error.message}`;
      });
      failureStage = "audioContext";
      const contextStatus = await contextReady;
      if (generation !== captureGeneration || muted || !voiceSettings?.listeningEnabled) {
        await stopCapture("Sleep", muted ? "Microphone is muted." : "Listening is paused.");
        return;
      }
      if (contextStatus.error) {
        throw contextStatus.error;
      }
      const actualSampleRate = contextStatus.sampleRate;
      updatePipelineDiagnostic(
        "audioContext",
        `running at ${actualSampleRate} Hz; Whisper input is 16000 Hz`,
      );
      failureStage = "audioWorklet";
      const workletStatus = await workletReady;
      if (workletStatus.error) {
        throw workletStatus.error;
      }
      updatePipelineDiagnostic("audioWorklet", "ready; waiting for microphone frames");
      if (generation !== captureGeneration || muted || !voiceSettings?.listeningEnabled) {
        await stopCapture("Sleep", muted ? "Microphone is muted." : "Listening is paused.");
        return;
      }
      audioSource = audioContext.createMediaStreamSource(mediaStream);
      audioNode = new AudioWorkletNode(audioContext, "jarvis-capture", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
      });
      const mutedOutput = audioContext.createGain();
      mutedOutput.gain.value = 0;
      segmenter = new voiceCore.VoiceActivitySegmenter({
        sampleRate: context.sampleRate,
        silenceMs: 400,
        maximumMs: 30000,
      });
      bargeInDetector = new voiceCore.VoiceOnsetDetector({
        sampleRate: context.sampleRate,
        minimumMs: 180,
      });
      let resolveFirstFrame;
      const firstAudioFrame = new Promise((resolve) => {
        resolveFirstFrame = resolve;
        cancelPendingFrameCheck = () => resolve(false);
      });
      audioNode.port.onmessage = ({ data }) => {
        const input = data instanceof Float32Array ? data : new Float32Array(data);
        if (generation !== captureGeneration || audioContext !== context || input.length === 0) {
          return;
        }
        audioFramesReceived += 1;
        capturedSamples += input.length;
        if (resolveFirstFrame) {
          resolveFirstFrame(true);
          resolveFirstFrame = undefined;
          cancelPendingFrameCheck = undefined;
        }
        const now = Date.now();
        if (now - lastPipelineRenderAt >= 500) {
          lastPipelineRenderAt = now;
          updatePipelineDiagnostic(
            "audioWorklet",
            `receiving ${audioFramesReceived} frames at ${context.sampleRate} Hz`,
          );
          updatePipelineDiagnostic(
            "capturedAudio",
            `${(capturedSamples / context.sampleRate).toFixed(2)} seconds received`,
          );
        }
        if (Date.now() - inputLevelUpdatedAt >= 125) {
          inputLevelUpdatedAt = Date.now();
          updateInputLevel(input);
        }
        if (voiceState === "Speaking") {
          if (!bargeInRequested && bargeInDetector.push(input)) {
            bargeInRequested = true;
            void interruptCurrentSpeech().catch((error) => {
              reportVoiceError("speech", new Error(`Could not interrupt speech: ${error.message}`));
            });
          }
        } else {
          bargeInDetector.reset();
          bargeInRequested = false;
        }
        const utterance = segmenter.push(input);
        if (segmenter.active) {
          updatePipelineDiagnostic(
            "vad",
            `speech detected; threshold ${segmenter.threshold.toFixed(3)} RMS`,
          );
        } else if (!utterance) {
          updatePipelineDiagnostic("vad", "waiting for speech");
        }
        if (utterance) {
          const durationSeconds = utterance.length / context.sampleRate;
          updatePipelineDiagnostic("lastUtterance", `${durationSeconds.toFixed(2)} seconds`);
          const speechLike = voiceCore.isSpeechLikeAudio(utterance, context.sampleRate);
          // #region agent log
          agentDebugLog("A", "renderer.js:audioFrame", speechLike ? "utterance accepted" : "utterance rejected", {
            durationSeconds: Number(durationSeconds.toFixed(2)),
            sampleRate: context.sampleRate,
            voiceState,
            processing,
            listening,
            activeConversation,
          });
          // #endregion
          if (speechLike) {
            updatePipelineDiagnostic("vad", "utterance accepted; sending to Whisper");
            queueTranscription(utterance, context.sampleRate);
          } else {
            updatePipelineDiagnostic("vad", "utterance rejected by speech-quality filter");
          }
        }
      };
      audioSource.connect(audioNode);
      audioNode.connect(mutedOutput);
      mutedOutput.connect(audioContext.destination);
      if (generation !== captureGeneration) {
        await stopCapture("Sleep", "Listening is paused.");
        return;
      }
      listening = true;
      updateMicrophoneButtons();
      failureStage = "audioWorklet";
      const receivedAudioFrame = await Promise.race([
        firstAudioFrame,
        new Promise((resolve, reject) => {
          frameTimeout = window.setTimeout(
            () => reject(new Error("AudioWorklet received no microphone audio frames within 3 seconds.")),
            3000,
          );
        }),
      ]);
      window.clearTimeout(frameTimeout);
      if (!receivedAudioFrame || generation !== captureGeneration) {
        return;
      }
      updatePipelineDiagnostic("audioWorklet", `receiving microphone frames (${audioFramesReceived} received)`);
      setVoiceState(activeConversation ? "Listening" : "Standby");
    } catch (error) {
      failed = true;
      window.clearTimeout(frameTimeout);
      // #region agent log
      agentDebugLog("A", "renderer.js:startCapture", "capture failed", {
        failureStage,
        error: error.message,
      });
      // #endregion
      await stopCapture("Error", `Could not start voice capture: ${error.message}`);
      reportVoiceError(failureStage, error);
    } finally {
      captureStartPromise = undefined;
      if (!failed && !listening && voiceSettings?.listeningEnabled && !muted && generation !== captureGeneration) {
        void startCapture();
      }
    }
  })();
  return captureStartPromise;
}

function spokenReply(command, reply) {
  if (languagePicker.value !== "hi-IN") {
    return reply;
  }
  if (reply.startsWith("Opening chrome")) {
    return "क्रोम खोल रहा हूँ।";
  }
  if (reply.startsWith("Opening vscode")) {
    return "वीएस कोड खोल रहा हूँ।";
  }
  if (reply.startsWith("Opening notepad")) {
    return "नोटपैड खोल रहा हूँ।";
  }
  if (reply.startsWith("Created ")) {
    return "फ़ाइल बना दी गई।";
  }
  if (reply.startsWith("Updated ")) {
    return "फ़ाइल अपडेट कर दी गई।";
  }
  if (reply.startsWith("Deleted ")) {
    return "फ़ाइल हटा दी गई।";
  }
  if (reply.startsWith("CPU:")) {
    return "सिस्टम की जानकारी बातचीत में दिखाई गई है।";
  }
  if (command.trim().toLowerCase() === "help") {
    return "कमांड सूची बातचीत में दिखाई गई है।";
  }
  return reply;
}

function updateSessionDiagnostic() {
  const status = activeConversation
    ? `active; follow-ups accepted (${processing ? "processing" : voiceState === "Speaking" ? "speaking" : "listening"})`
    : "inactive; wake phrase required";
  updatePipelineDiagnostic("session", status);
}

function refreshConversationSession() {
  activeConversation = true;
  updateSessionDiagnostic();
  document.body.classList.add("is-awake");
  window.clearTimeout(conversationTimer);
  // #region agent log
  agentDebugLog("A", "renderer.js:refreshConversationSession", "conversation session armed", {
    timeoutMs: 45000,
    processing,
    voiceState,
    listening,
  });
  // #endregion
  conversationTimer = window.setTimeout(() => {
    if (processing || voiceState === "Speaking") {
      // #region agent log
      agentDebugLog("B", "renderer.js:refreshConversationSession", "session timer re-armed instead of ending", {
        processing,
        voiceState,
        listening,
      });
      // #endregion
      refreshConversationSession();
      return;
    }
    activeConversation = false;
    updateSessionDiagnostic();
    document.body.classList.remove("is-awake");
    // #region agent log
    agentDebugLog("A", "renderer.js:refreshConversationSession", "conversation session ended", {
      listening,
      voiceState,
    });
    // #endregion
    if (listening) {
      setVoiceState("Standby");
    }
  }, 45000);
}

function isPausePhrase(text) {
  const normalized = voiceCore.normalizeSpeechText(text);
  return [
    "stop listening", "pause listening", "go to sleep",
    "सुनना बंद करो", "सुनना बंद कर", "चुप हो जाओ",
  ].some((phrase) => normalized.includes(phrase));
}

function queueTranscription(samples, sampleRate) {
  transcriptionQueue = transcriptionQueue.then(async () => {
    if (!listening) {
      // #region agent log
      agentDebugLog("E", "renderer.js:queueTranscription", "transcription skipped because listening is off", {
        voiceState,
        processing,
        activeConversation,
      });
      // #endregion
      return;
    }
    let failed = false;
    const durationSeconds = samples.length / sampleRate;
    updatePipelineDiagnostic("transcription", `encoding ${durationSeconds.toFixed(2)} second utterance`);
    if (voiceState !== "Speaking") {
      setVoiceState("Thinking", "Checking speech locally.");
    }
    const transcriptionStartedAt = Date.now();
    try {
      const mono16k = voiceCore.resampleTo16k(samples, sampleRate);
      const wav = voiceCore.encodeWav(
        mono16k,
        voiceCore.TARGET_SAMPLE_RATE,
      );
      updatePipelineDiagnostic(
        "whisper",
        `whisper-cli.exe running; ${voiceSettings?.whisperModel || "base"} model; ${sampleRate} Hz → 16000 Hz; ${wav.byteLength} byte WAV`,
      );
      updatePipelineDiagnostic("transcription", "transcribing offline with Whisper");
      const transcript = await window.jarvis.transcribeAudio(wav, transcriptionLanguage());
      latencyMetrics.transcriptionMs = Date.now() - transcriptionStartedAt;
      renderLatencyMetrics();
      showRawTranscript(transcript);
      updatePipelineDiagnostic(
        "transcription",
        transcript ? `transcript received in ${latencyMetrics.transcriptionMs} ms` : "Whisper returned no text",
      );
      const transcriptValid = voiceCore.isValidTranscript(transcript);
      // #region agent log
      agentDebugLog("B", "renderer.js:queueTranscription", "transcript received", {
        valid: transcriptValid,
        transcriptPreview: String(transcript || "").slice(0, 120),
        elapsedMs: Date.now() - transcriptionStartedAt,
        voiceState,
        processing,
        activeConversation,
      });
      // #endregion
      if (!transcriptValid) {
        updatePipelineDiagnostic("wakeWord", "transcript rejected as empty, invalid, or hallucinated");
        return;
      }
      const now = Date.now();
      if (voiceCore.isDuplicateTranscript(
        transcript,
        lastAcceptedTranscript,
        now - lastAcceptedTranscriptAt,
      )) {
        updatePipelineDiagnostic("transcription", "duplicate transcript suppressed");
        return;
      }
      if (await handleTranscript(transcript)) {
        lastAcceptedTranscript = transcript;
        lastAcceptedTranscriptAt = now;
      } else {
        updatePipelineDiagnostic("wakeWord", "wake phrase not detected; transcript ignored");
      }
    } catch (error) {
      failed = true;
      reportVoiceError("transcription", error);
    } finally {
      if (voiceCore.shouldRestoreListeningStatus({ listening, voiceState, processing, failed })) {
        setVoiceState(activeConversation ? "Listening" : "Standby");
      }
    }
  }).catch((error) => {
    reportVoiceError("transcription", new Error(`Offline transcription queue failed: ${error.message}`));
  });
}

function enqueueCommand(command, spoken = true, originalTranscript = command) {
  commandQueue = commandQueue.then(() => executeCommand(command, spoken, originalTranscript));
  return commandQueue;
}

async function handleTranscript(transcript) {
  const preservedTranscript = String(transcript || "");
  const safeTranscript = voiceCore.sanitizeTranscript(preservedTranscript);
  if (!listening || !safeTranscript) {
    // #region agent log
    agentDebugLog("E", "renderer.js:handleTranscript", "transcript dropped before command parsing", {
      listening,
      hasTranscript: Boolean(safeTranscript),
      voiceState,
      processing,
    });
    // #endregion
    return false;
  }
  const mayBeEcho = voiceState === "Speaking" || Date.now() < spokenEchoWindowUntil;
  if (mayBeEcho && voiceCore.resemblesEcho(safeTranscript, currentSpokenText)) {
    // #region agent log
    agentDebugLog("E", "renderer.js:handleTranscript", "transcript rejected as echo", {
      voiceState,
      activeConversation,
      transcriptPreview: safeTranscript.slice(0, 120),
    });
    // #endregion
    return false;
  }
  if (voiceState === "Speaking") {
    await interruptCurrentSpeech();
  }
  const wake = voiceCore.extractWakeCommand(safeTranscript);
  const hadActiveConversation = activeConversation;
  updatePipelineDiagnostic(
    "wakeWord",
    wake.activated ? "wake phrase recognized" : hadActiveConversation ? "follow-up accepted in active session" : "wake phrase not detected",
  );
  const resolvedCommand = voiceCore.resolveConversationCommand(safeTranscript, activeConversation);
  const command = resolvedCommand === null ? null : voiceCore.normalizeCommand(resolvedCommand);
  // #region agent log
  agentDebugLog("C", "renderer.js:handleTranscript", "wake and command decision", {
    wakeActivated: wake.activated,
    hadActiveConversation,
    commandPreview: command === null ? null : String(command).slice(0, 120),
    transcriptPreview: safeTranscript.slice(0, 120),
    voiceState,
    listening,
  });
  // #endregion
  if (command === null) {
    return false;
  }
  if (processing && !wake.activated) {
    // #region agent log
    agentDebugLog("D", "renderer.js:handleTranscript", "follow-up skipped while a command is still running", {
      commandPreview: String(command).slice(0, 120),
    });
    // #endregion
    return true;
  }
  if (isPausePhrase(command || safeTranscript)) {
    await window.jarvis.setVoiceControl("pause");
    updatePipelineDiagnostic("wakeWord", "pause phrase recognized");
    return true;
  }
  refreshConversationSession();
  if (!command) {
    updatePipelineDiagnostic("wakeWord", "wake phrase accepted; waiting for a command");
    await speak(languagePicker.value === "hi-IN" ? "जी, बताइए।" : "Yes, I'm listening.");
    return true;
  }
  void enqueueCommand(command, true, preservedTranscript).catch((error) => {
    setVoiceState("Error", `Could not queue the spoken command: ${error.message}`);
  });
  return true;
}

function addIntegrationButton(container, action, account) {
  const button = document.createElement("button");
  button.type = "button";
  button.dataset.integrationAction = action;
  if (account) {
    button.dataset.account = account;
  }
  const labels = {
    connect: "Connect",
    configure: "Configure",
    test: "Test connection",
    disconnect: "Disconnect",
    "emergency-stop-on": "Emergency stop",
    "emergency-stop-off": "Resume communications",
  };
  button.textContent = labels[action];
  container.append(button);
}

function renderIntegrations(statuses) {
  integrationsList.replaceChildren();
  for (const status of statuses || []) {
    const card = document.createElement("article");
    card.className = "integration-card";

    const title = document.createElement("h3");
    title.textContent = status.name;
    card.append(title);

    const connectedAccounts = Array.isArray(status.accounts) ? status.accounts : [];
    const summary = document.createElement("p");
    summary.textContent = connectedAccounts.length
      ? `${connectedAccounts.length} account${connectedAccounts.length === 1 ? "" : "s"} connected`
      : status.configured
        ? "OAuth configured; no account connected"
        : "OAuth client not configured";
    card.append(summary);
    if (status.emergencyStopped) {
      const notice = document.createElement("p");
      notice.textContent = "Emergency stop is active.";
      card.append(notice);
    }

    const capabilities = document.createElement("ul");
    capabilities.className = "integration-capabilities";
    for (const capability of status.capabilities || []) {
      const item = document.createElement("li");
      item.textContent = capability;
      capabilities.append(item);
    }
    card.append(capabilities);

    const providerActions = document.createElement("div");
    providerActions.className = "integration-actions";
    if (status.provider === "google") {
      addIntegrationButton(providerActions, "connect");
      addIntegrationButton(providerActions, status.emergencyStopped ? "emergency-stop-off" : "emergency-stop-on");
    }
    addIntegrationButton(providerActions, "configure");
    card.append(providerActions);

    for (const account of connectedAccounts) {
      const accountRow = document.createElement("p");
      accountRow.textContent = account.email;
      card.append(accountRow);
      const accountActions = document.createElement("div");
      accountActions.className = "integration-actions";
      addIntegrationButton(accountActions, "test", account.email);
      addIntegrationButton(accountActions, "disconnect", account.email);
      card.append(accountActions);
    }
    integrationsList.append(card);
  }
}

async function loadIntegrations() {
  try {
    const result = await window.jarvis.getIntegrations();
    renderIntegrations(result.data);
  } catch (error) {
    integrationsList.replaceChildren();
    const message = document.createElement("p");
    message.className = "empty-state";
    message.textContent = `Could not load connections: ${error.message}`;
    integrationsList.append(message);
  }
}

function renderTasks(tasks) {
  backgroundTasks.replaceChildren();
  if (!tasks.length) {
    const empty = document.createElement("p");
    empty.className = "empty-state";
    empty.textContent = "No background tasks yet.";
    backgroundTasks.append(empty);
    return;
  }
  for (const task of tasks) {
    const row = document.createElement("article");
    row.className = "background-task";
    const details = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = task.description;
    const status = document.createElement("p");
    status.textContent = `${task.status}${task.output ? ` — ${task.output}` : ""}${task.error ? ` — ${task.error}` : ""}`;
    details.append(title, status);
    row.append(details);
    if (task.status === "queued" || task.status === "running") {
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.textContent = "Cancel";
      cancel.dataset.cancelTask = task.id;
      row.append(cancel);
    }
    backgroundTasks.append(row);
  }
}

async function pollBackgroundTasks() {
  if (taskPollPending) {
    return;
  }
  taskPollPending = true;
  try {
    const response = await window.jarvis.getTasks();
    const tasks = Array.isArray(response.data) ? response.data : [];
    renderTasks(tasks);
    for (const task of tasks) {
      const previousStatus = knownTaskStates.get(task.id);
      const createdDuringSession = Date.parse(task.createdAt) >= rendererStartedAt;
      const changedSinceObserved = taskBaselineLoaded && previousStatus && previousStatus !== task.status;
      if (task.status === "completed" && task.output && !announcedTaskIds.has(task.id) &&
          (changedSinceObserved || (!previousStatus && createdDuringSession))) {
        announcedTaskIds.add(task.id);
        addMessage("assistant", task.output);
        const notificationKind = task.description?.toLowerCase().startsWith("reminder:")
          ? "reminder"
          : "task";
        const communicationPreferences = voiceSettings || voiceCore.VOICE_PREFERENCE_DEFAULTS;
        const canSpeakNotification = voiceCore.shouldSpeakProactive(
          communicationPreferences,
          notificationKind,
          new Date(),
          lastSuggestionAt,
        );
        if (canSpeakNotification && !processing && voiceState !== "Speaking" && !activeConversation) {
          try {
            await speak(task.output);
          } catch (error) {
            setVoiceState("Error", `Task notification speech failed: ${error.message}`);
          }
        }
        const suggestion = voiceCore.completionSuggestion(task);
        const canSuggest = suggestion && !processing && voiceState !== "Speaking" && !activeConversation &&
          voiceCore.shouldSpeakProactive(communicationPreferences, "suggestion", new Date(), lastSuggestionAt);
        if (canSuggest) {
          lastSuggestionAt = Date.now();
          void persistVoiceSetting("lastSuggestionAt", lastSuggestionAt).catch(() => {});
          addMessage("assistant", suggestion);
          try {
            await speak(suggestion);
          } catch (error) {
            setVoiceState("Error", `Proactive suggestion speech failed: ${error.message}`);
          }
        }
      } else if ((task.status === "failed" || task.status === "timed_out") &&
          !announcedTaskIds.has(task.id) &&
          (changedSinceObserved || (!previousStatus && createdDuringSession))) {
        announcedTaskIds.add(task.id);
        addMessage("assistant", `${task.description}: ${task.error}`);
      }
      knownTaskStates.set(task.id, task.status);
    }
    taskBaselineLoaded = true;
    const active = tasks.some((task) => task.status === "queued" || task.status === "running");
    if (active && !processing && voiceState !== "Speaking" && voiceState !== "Error") {
      setVoiceState("Working in background", "JARVIS is running a background task.");
    } else if (!active && voiceState === "Working in background") {
      setVoiceState(listening ? (activeConversation ? "Listening" : "Standby") : "Sleep");
    }
  } catch (error) {
    backgroundTasks.replaceChildren();
    const message = document.createElement("p");
    message.className = "empty-state";
    message.textContent = `Could not load background tasks: ${error.message}`;
    backgroundTasks.append(message);
  } finally {
    taskPollPending = false;
  }
}

async function runIntegrationAction(action, account) {
  let command;
  if (action === "connect") {
    command = "connect google";
  } else if (action === "emergency-stop-on") {
    command = "integration emergency-stop on";
  } else if (action === "emergency-stop-off") {
    command = "integration emergency-stop off";
  } else if (action === "configure") {
    command = "configure google";
  } else if (action === "test" && account) {
    command = `test google "${account}"`;
  } else if (action === "disconnect" && account) {
    command = `disconnect google "${account}"`;
  } else {
    return;
  }
  await enqueueCommand(command, false);
  await loadIntegrations();
}

async function executeCommand(command, spoken = true, originalTranscript = command) {
  const trimmed = command.trim();
  if (!trimmed) {
    return;
  }

  activeCommandStartedAt = Date.now();
  latencyMetrics.firstAiTokenMs = null;
  latencyMetrics.firstSpeechMs = null;
  renderLatencyMetrics();
  const speechQueue = spoken ? new voiceCore.StreamingSpeechQueue({
    speak,
    interrupt: () => window.jarvis.interruptSpeech(),
    onError: (error) => reportVoiceError("speech", new Error(`Spoken response was stopped: ${error.message}`)),
  }) : undefined;
  activeSpeechQueue = speechQueue;
  processing = true;
  // #region agent log
  agentDebugLog("D", "renderer.js:executeCommand", "spoken or typed command started", {
    spoken,
    commandPreview: trimmed.slice(0, 120),
    listening,
    voiceState,
    activeConversation,
  });
  // #endregion
  discardAssistantStream();
  document.body.classList.remove("is-awake");
  addMessage("user", originalTranscript || trimmed);
  setVoiceState("Thinking", "Working on your request.");
  try {
    updatePipelineDiagnostic("backend", `${spoken ? "spoken" : "typed"} command sent to Go backend`);
    updatePipelineDiagnostic("ai", "dispatched to Go backend; awaiting response");
    const result = await window.jarvis.executeCommand(trimmed, true);
    updatePipelineDiagnostic("backend", "Go backend responded successfully");
    updatePipelineDiagnostic(
      "ai",
      latencyMetrics.firstAiTokenMs === null
        ? "handled locally; no Ollama call needed"
        : "Ollama response completed",
    );
    const reply = result.output || "Done.";
    // #region agent log
    agentDebugLog("D", "renderer.js:executeCommand", "command completed", {
      replyPreview: String(reply).slice(0, 160),
      spoken,
      voiceState,
    });
    // #endregion
    finishAssistantStream(reply);
    if (spoken) {
      if (speechQueue && !speechQueue.hasContent && !speechQueue.cancelled) {
        speechQueue.push(spokenReply(trimmed, reply));
      }
      await speechQueue?.finish();
    }
  } catch (error) {
    // #region agent log
    agentDebugLog("D", "renderer.js:executeCommand", "command failed in renderer", {
      error: error.message,
      voiceState,
      listening,
      activeConversation,
    });
    // #endregion
    const interrupted = speechQueue?.cancelled === true;
    updatePipelineDiagnostic("ai", interrupted ? "cancelled by user" : "request failed; see backend error");
    if (!pipelineDiagnostics.backend.includes("responded successfully")) {
      reportVoiceError("backend", error);
    }
    await speechQueue?.cancel();
    discardAssistantStream();
    addMessage("assistant", error.message);
    setVoiceState("Error", "I couldn't complete that command.");
    if (spoken && !interrupted) {
      try {
        await speak(languagePicker.value === "hi-IN"
          ? `कमांड पूरा नहीं हो सका। ${error.message}`
          : error.message);
      } catch (speechError) {
        reportVoiceError("speech", speechError);
      }
    }
  } finally {
    processing = false;
    if (activeSpeechQueue === speechQueue) {
      activeSpeechQueue = undefined;
    }
    activeCommandStartedAt = 0;
    if (listening && activeConversation) {
      // #region agent log
      agentDebugLog("C", "renderer.js:executeCommand", "session refreshed after command finished", {
        listening,
        voiceState,
        activeConversation,
      });
      // #endregion
      refreshConversationSession();
    }
    if (listening && voiceState !== "Speaking" && voiceState !== "Error") {
      setVoiceState(activeConversation ? "Listening" : "Standby");
    }
  }
}

microphoneButton.addEventListener("click", () => {
  const shouldResume = !voiceSettings?.listeningEnabled ||
    (voiceState === "Error" && !listening && !captureStartPromise && !muted);
  if (shouldResume) {
    voiceSettings.listeningEnabled = true;
    updateMicrophoneButtons();
    if (!muted) {
      void startCapture();
    }
    void persistVoiceSetting("listeningEnabled", true).catch(async (error) => {
      voiceSettings.listeningEnabled = false;
      await stopCapture("Error", error.message);
      reportVoiceError("microphone", error);
    });
    return;
  }
  void window.jarvis.setVoiceControl("pause")
    .catch((error) => setVoiceState("Error", error.message));
});

muteButton.addEventListener("click", () => {
  void window.jarvis.setVoiceControl(muted ? "unmute" : "mute")
    .catch((error) => setVoiceState("Error", error.message));
});

window.jarvis.onVoiceAction((action) => {
  if (!voiceSettings) {
    return;
  }
  if (action === "resume") {
    voiceSettings.listeningEnabled = true;
    void startCapture();
  } else if (action === "pause") {
    voiceSettings.listeningEnabled = false;
    void stopCapture("Sleep", "Listening is paused.");
  } else if (action === "mute") {
    muted = true;
    voiceSettings.muted = true;
    void stopCapture("Sleep", "Microphone is muted.");
  } else if (action === "unmute") {
    muted = false;
    voiceSettings.muted = false;
    if (voiceSettings.listeningEnabled) {
      void startCapture();
    } else {
      void stopCapture("Sleep", "Listening is paused.");
    }
  }
  updateMicrophoneButtons();
});

window.addEventListener("unhandledrejection", (event) => {
  reportVoiceError("renderer", event.reason || new Error("Unhandled renderer promise rejection."));
});

window.addEventListener("error", (event) => {
  reportVoiceError("renderer", new Error(event.message || "Unhandled renderer error."));
});

commandForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void enqueueCommand(commandInput.value, false);
  commandInput.value = "";
});

document.querySelectorAll("[data-command]").forEach((button) => {
  button.addEventListener("click", () => {
    void enqueueCommand(button.dataset.command, false);
  });
});

document.querySelector("#clear-button").addEventListener("click", async () => {
  try {
    await window.jarvis.clearConversation();
    conversation.replaceChildren();
    const emptyState = document.createElement("p");
    emptyState.className = "empty-state";
    emptyState.textContent = "Your recent commands and JARVIS responses will appear here.";
    conversation.append(emptyState);
  } catch (error) {
    setStatus(`Could not clear conversation: ${error.message}`);
  }
});

document.querySelector("#workspace-button").addEventListener("click", async () => {
  try {
    const selected = await window.jarvis.chooseWorkspace();
    workspacePath.textContent = selected;
    setStatus("Workspace updated");
  } catch (error) {
    setStatus(`Could not change workspace: ${error.message}`);
  }
});

document.querySelector("#integrations-button").addEventListener("click", () => {
  document.querySelector(".integrations-panel").scrollIntoView({ behavior: "smooth", block: "center" });
});
microphonePicker.addEventListener("change", () => {
  const selected = microphonePicker.value;
  void persistVoiceSetting("microphoneDeviceId", selected).then(async () => {
    if (voiceSettings?.listeningEnabled && !muted) {
      if (listening) {
        await stopCapture();
      }
      await startCapture();
    } else if (listening) {
      await stopCapture();
    }
  }).catch(() => {});
});
whisperModelPicker.addEventListener("change", () => {
  void persistVoiceSetting("whisperModel", whisperModelPicker.value).then((updated) => {
    updateWhisperDiagnostics(updated.runtime);
    renderVoiceSettings();
  }).catch(() => {
    renderVoiceSettings();
  });
});
for (const [selector, key] of [
  ["#proactive-enabled", "proactiveSuggestionsEnabled"],
  ["#dnd-enabled", "doNotDisturb"],
  ["#task-announcements", "announceTaskCompletions"],
  ["#reminder-announcements", "announceReminders"],
]) {
  document.querySelector(selector).addEventListener("change", (event) => {
    void persistVoiceSetting(key, event.currentTarget.checked).catch(() => {
      renderVoiceSettings();
    });
  });
}
for (const [selector, key] of [
  ["#quiet-hours-start", "quietHoursStart"],
  ["#quiet-hours-end", "quietHoursEnd"],
]) {
  document.querySelector(selector).addEventListener("change", (event) => {
    void persistVoiceSetting(key, event.currentTarget.value).catch(() => {
      renderVoiceSettings();
    });
  });
}
document.querySelector("#proactive-cooldown").addEventListener("change", (event) => {
  void persistVoiceSetting("proactiveCooldownMinutes", Number(event.currentTarget.value)).catch(() => {
    renderVoiceSettings();
  });
});
diagnosticsButton.addEventListener("click", () => {
  void runPerformanceDiagnostics();
});
document.querySelector("#refresh-integrations").addEventListener("click", () => {
  void loadIntegrations();
});
integrationsList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-integration-action]");
  if (button) {
    void runIntegrationAction(button.dataset.integrationAction, button.dataset.account);
  }
});
backgroundTasks.addEventListener("click", (event) => {
  const button = event.target.closest("[data-cancel-task]");
  if (button) {
    void enqueueCommand(`cancel task ${button.dataset.cancelTask}`, false);
  }
});

window.jarvis.onVoiceState((update) => {
  if (update?.state && update.state !== voiceState) {
    voiceState = update.state;
    statusText.textContent = update.state;
    updateMicrophoneButtons();
    if (update.detail) {
      voiceDetail.textContent = update.detail;
    }
  }
  if (update?.state === "Speaking") {
    updatePipelineDiagnostic("speech", "Windows SAPI reports active playback");
  }
  if (update?.state === "Speaking" && activeCommandStartedAt && latencyMetrics.firstSpeechMs === null) {
    latencyMetrics.firstSpeechMs = Date.now() - activeCommandStartedAt;
    renderLatencyMetrics();
  }
});

window.jarvis.onAssistantDelta((update) => {
  const delta = update?.delta;
  appendAssistantDelta(delta);
  if (typeof delta === "string" && activeCommandStartedAt && latencyMetrics.firstAiTokenMs === null) {
    latencyMetrics.firstAiTokenMs = Date.now() - activeCommandStartedAt;
    updatePipelineDiagnostic("ai", "Ollama streaming response received");
    renderLatencyMetrics();
  }
  if (typeof delta === "string" && activeSpeechQueue) {
    activeSpeechQueue.push(delta);
  }
});

async function initializeDesktop() {
  renderPipelineDiagnostics();
  try {
    voiceSettings = await window.jarvis.getVoiceSettings();
    muted = voiceSettings.muted;
    lastSuggestionAt = voiceSettings.lastSuggestionAt || 0;
    updateWhisperDiagnostics();
    if (voiceSettings.diagnosticLogPath) {
      updatePipelineDiagnostic("diagnosticLog", voiceSettings.diagnosticLogPath);
    }
    renderVoiceSettings();
    renderLatencyMetrics();
    try {
      await refreshMicrophoneDevices();
    } catch (error) {
      setVoiceState("Error", `Could not enumerate microphones: ${error.message}`);
    }
    updateMicrophoneButtons();
    if (voiceSettings.listeningEnabled && !muted) {
      await startCapture();
    } else {
      await stopCapture("Sleep", muted ? "Microphone is muted." : "Listening is paused.");
    }
  } catch (error) {
    setVoiceState("Error", `Could not initialize voice controls: ${error.message}`);
  }
  window.jarvis.getWorkspace().then((workspace) => {
    workspacePath.textContent = workspace;
  }).catch((error) => {
    setStatus(`Could not load workspace: ${error.message}`);
  });
  void loadIntegrations();
  await pollBackgroundTasks();
  taskPollTimer = window.setInterval(() => {
    void pollBackgroundTasks();
  }, 5000);
}

if (navigator.mediaDevices?.addEventListener) {
  navigator.mediaDevices.addEventListener("devicechange", () => {
    void refreshMicrophoneDevices().catch((error) => {
      setVoiceState("Error", `Could not refresh microphone devices: ${error.message}`);
    });
  });
}

void initializeDesktop();
