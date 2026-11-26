const voiceCore = window.JarvisVoiceCore;
const microphoneButton = document.querySelector("#microphone-button");
const microphoneLabel = document.querySelector("#microphone-label");
const muteButton = document.querySelector("#mute-button");
const languagePicker = document.querySelector("#language");
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
let knownTaskStates = new Map();
let taskPollTimer;
let taskBaselineLoaded = false;
let taskPollPending = false;
const rendererStartedAt = Date.now();
const announcedTaskIds = new Set();
let captureGeneration = 0;
let captureStartPromise;

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
  bargeInDetector?.reset();
  bargeInRequested = false;
  currentSpokenText = text;
  spokenEchoWindowUntil = Number.POSITIVE_INFINITY;
  setVoiceState("Speaking");
  try {
    await window.jarvis.speak(text);
  } finally {
    spokenEchoWindowUntil = Date.now() + 5000;
  }
}

function updateMicrophoneButtons() {
  microphoneLabel.textContent = voiceSettings?.listeningEnabled ? "Pause listening" : "Resume listening";
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
  return "auto";
}

async function stopCapture(nextState = "Sleep", detail = "") {
  captureGeneration += 1;
  listening = false;
  activeConversation = false;
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
  document.body.classList.remove("is-listening", "is-awake");
  updateMicrophoneButtons();
  setVoiceState(nextState, detail);
}

async function startCapture() {
  if (listening) {
    return;
  }
  if (captureStartPromise) {
    return captureStartPromise;
  }
  if (muted || !voiceSettings?.listeningEnabled) {
    await stopCapture("Sleep", muted ? "Microphone is muted." : "Listening is paused.");
    return;
  }
  if (!voiceSettings?.runtime?.available) {
    await stopCapture("Offline", voiceSettings?.runtime?.message || "Configure offline voice recognition.");
    return;
  }
  const generation = ++captureGeneration;
  captureStartPromise = (async () => {
    let failed = false;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });
      if (generation !== captureGeneration || muted || !voiceSettings?.listeningEnabled) {
        for (const track of stream.getTracks()) {
          track.stop();
        }
        return;
      }
      mediaStream = stream;
      audioContext = new AudioContext({ sampleRate: voiceCore.TARGET_SAMPLE_RATE });
      await audioContext.audioWorklet.addModule("voice-capture-worklet.js");
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
      segmenter = new voiceCore.VoiceActivitySegmenter({ sampleRate: audioContext.sampleRate });
      bargeInDetector = new voiceCore.VoiceOnsetDetector({
        sampleRate: voiceCore.TARGET_SAMPLE_RATE,
        minimumMs: 180,
      });
      audioNode.port.onmessage = ({ data }) => {
        const input = data instanceof Float32Array ? data : new Float32Array(data);
        const samples = voiceCore.resampleTo16k(input, audioContext.sampleRate);
        if (voiceState === "Speaking") {
          if (!bargeInRequested && bargeInDetector.push(samples)) {
            bargeInRequested = true;
            void window.jarvis.interruptSpeech().catch((error) => {
              setVoiceState("Error", `Could not interrupt speech: ${error.message}`);
            });
          }
        } else {
          bargeInDetector.reset();
          bargeInRequested = false;
        }
        const utterance = segmenter.push(samples);
        if (utterance) {
          queueTranscription(utterance);
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
      setVoiceState(activeConversation ? "Listening" : "Standby");
    } catch (error) {
      failed = true;
      await stopCapture("Error", `Could not start the microphone: ${error.message}`);
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

function refreshConversationSession() {
  activeConversation = true;
  document.body.classList.add("is-awake");
  window.clearTimeout(conversationTimer);
  conversationTimer = window.setTimeout(() => {
    activeConversation = false;
    document.body.classList.remove("is-awake");
    if (listening && voiceState !== "Speaking" && !processing) {
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

function queueTranscription(samples) {
  transcriptionQueue = transcriptionQueue.then(async () => {
    if (!listening) {
      return;
    }
    if (voiceState !== "Speaking") {
      setVoiceState("Thinking", "Checking speech locally.");
    }
    try {
      const wav = voiceCore.encodeWav(
        voiceCore.resampleTo16k(samples, voiceCore.TARGET_SAMPLE_RATE),
        voiceCore.TARGET_SAMPLE_RATE,
      );
      const transcript = await window.jarvis.transcribeAudio(wav, transcriptionLanguage());
      if (transcript) {
        await handleTranscript(transcript);
      }
    } catch (error) {
      setVoiceState("Error", error.message);
    } finally {
      if (listening && voiceState !== "Speaking" && !processing) {
        setVoiceState(activeConversation ? "Listening" : "Standby");
      }
    }
  }).catch((error) => {
    setVoiceState("Error", `Offline transcription queue failed: ${error.message}`);
  });
}

function enqueueCommand(command, spoken = true) {
  commandQueue = commandQueue.then(() => executeCommand(command, spoken));
  return commandQueue;
}

async function handleTranscript(transcript) {
  if (!listening || !transcript.trim()) {
    return;
  }
  const mayBeEcho = voiceState === "Speaking" || Date.now() < spokenEchoWindowUntil;
  if (mayBeEcho && voiceCore.resemblesEcho(transcript, currentSpokenText)) {
    return;
  }
  if (voiceState === "Speaking") {
    await window.jarvis.interruptSpeech();
    currentSpokenText = "";
    spokenEchoWindowUntil = 0;
  }
  const command = voiceCore.resolveConversationCommand(transcript, activeConversation);
  if (command === null) {
    return;
  }
  if (isPausePhrase(command || transcript)) {
    await window.jarvis.setVoiceControl("pause");
    return;
  }
  refreshConversationSession();
  if (!command) {
    await speak(languagePicker.value === "hi-IN" ? "जी, बताइए।" : "Yes, I'm listening.");
    return;
  }
  await enqueueCommand(command);
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
        try {
          await speak(task.output);
        } catch (error) {
          setVoiceState("Error", `Task notification speech failed: ${error.message}`);
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

async function executeCommand(command, spoken = true) {
  const trimmed = command.trim();
  if (!trimmed) {
    return;
  }

  processing = true;
  discardAssistantStream();
  document.body.classList.remove("is-awake");
  addMessage("user", trimmed);
  setVoiceState("Thinking", "Working on your request.");
  try {
    const result = await window.jarvis.executeCommand(trimmed, true);
    const reply = result.output || "Done.";
    finishAssistantStream(reply);
    if (spoken) {
      try {
        await speak(spokenReply(trimmed, reply));
      } catch (error) {
        setVoiceState("Error", `Speech output failed: ${error.message}`);
      }
    }
  } catch (error) {
    discardAssistantStream();
    addMessage("assistant", error.message);
    setVoiceState("Error", "I couldn't complete that command.");
    if (spoken) {
      try {
        await speak(languagePicker.value === "hi-IN"
          ? `कमांड पूरा नहीं हो सका। ${error.message}`
          : error.message);
      } catch (speechError) {
        setVoiceState("Error", `Speech output failed: ${speechError.message}`);
      }
    }
  } finally {
    processing = false;
    if (listening && voiceState !== "Speaking" && voiceState !== "Error") {
      setVoiceState(activeConversation ? "Listening" : "Standby");
    }
  }
}

microphoneButton.addEventListener("click", () => {
  void window.jarvis.setVoiceControl(voiceSettings?.listeningEnabled ? "pause" : "resume")
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
    if (update.detail) {
      voiceDetail.textContent = update.detail;
    }
  }
});

window.jarvis.onAssistantDelta((update) => {
  appendAssistantDelta(update?.delta);
});

async function initializeDesktop() {
  try {
    voiceSettings = await window.jarvis.getVoiceSettings();
    muted = voiceSettings.muted;
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
  }, 3000);
}

void initializeDesktop();
