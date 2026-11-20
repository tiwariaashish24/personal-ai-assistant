const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
const microphoneButton = document.querySelector("#microphone-button");
const microphoneLabel = document.querySelector("#microphone-label");
const languagePicker = document.querySelector("#language");
const statusText = document.querySelector("#status");
const conversation = document.querySelector("#conversation");
const commandForm = document.querySelector("#command-form");
const commandInput = document.querySelector("#command-input");
const workspacePath = document.querySelector("#workspace-path");
const microphonePrivacy = document.querySelector("#microphone-privacy");

let recognition;
let listening = false;
let awaitingCommandUntil = 0;
let processing = false;
let restartTimer;

function setStatus(message) {
  statusText.textContent = message;
}

function addMessage(role, text) {
  conversation.querySelector(".empty-state")?.remove();
  const message = document.createElement("div");
  message.className = `message ${role}`;
  message.textContent = text;
  conversation.append(message);
  conversation.scrollTop = conversation.scrollHeight;
}

function speak(text) {
  if (!text || !("speechSynthesis" in window)) {
    return;
  }
  window.speechSynthesis.cancel();
  const utterance = new SpeechSynthesisUtterance(text);
  utterance.lang = languagePicker.value;
  utterance.rate = 0.96;
  utterance.pitch = 0.92;
  const language = languagePicker.value.split("-")[0].toLowerCase();
  const voice = window.speechSynthesis.getVoices().find((candidate) =>
    candidate.lang.toLowerCase().startsWith(language),
  );
  if (voice) {
    utterance.voice = voice;
  }
  window.speechSynthesis.speak(utterance);
}

function setListening(value) {
  listening = value;
  document.body.classList.toggle("is-listening", value);
  microphoneLabel.textContent = value ? "Stop listening" : "Start listening";
  microphonePrivacy.textContent = value
    ? "Microphone is on while listening is enabled"
    : "Microphone is off until you start listening";
  if (!value) {
    document.body.classList.remove("is-awake");
    awaitingCommandUntil = 0;
    window.clearTimeout(restartTimer);
  }
}

function stopListening(message, announce = true) {
  setListening(false);
  if (recognition) {
    recognition.stop();
  }
  setStatus("Microphone paused");
  if (message) {
    addMessage("assistant", message);
    if (announce) {
      speak(message);
    }
  }
}

function startListening() {
  if (!recognition) {
    setStatus("Speech recognition is not available in this runtime.");
    addMessage("assistant", "Speech recognition is unavailable. Try a supported speech runtime or type a command.");
    return;
  }

  recognition.lang = languagePicker.value;
  try {
    recognition.start();
    setListening(true);
    setStatus("Listening for Hey Jarvis");
  } catch (error) {
    setStatus(`Could not start the microphone: ${error.message}`);
  }
}

function normalizeText(text) {
  return text.toLocaleLowerCase().replace(/[.,!?،؛؟]/g, " ").replace(/\s+/g, " ").trim();
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

function isStopCommand(text) {
  const normalized = normalizeText(text);
  return [
    "stop listening",
    "pause listening",
    "go to sleep",
    "सुनना बंद करो",
    "सुनना बंद कर",
    "चुप हो जाओ",
    "रुक जाओ",
  ].some((phrase) => normalized.includes(phrase));
}

function removeWakeWord(text) {
  const candidates = [
    /hey\s+jarvis/i,
    /jarvis/i,
    /हे\s*जार्विस/i,
    /जार्विस/i,
  ];
  for (const pattern of candidates) {
    const match = pattern.exec(text);
    if (match) {
      return text.slice(0, match.index) + text.slice(match.index + match[0].length);
    }
  }
  return null;
}

async function executeCommand(command, spoken = true) {
  if (processing) {
    return;
  }
  const trimmed = command.trim();
  if (!trimmed) {
    return;
  }

  processing = true;
  document.body.classList.remove("is-awake");
  addMessage("user", trimmed);
  setStatus("Working on it…");
  try {
    const result = await window.jarvis.executeCommand(trimmed);
    const reply = result.output || "Done.";
    addMessage("assistant", reply);
    setStatus(listening ? "Listening for Hey Jarvis" : "Ready");
    if (spoken) {
      speak(spokenReply(trimmed, reply));
    }
  } catch (error) {
    addMessage("assistant", error.message);
    setStatus("I couldn't complete that command.");
    if (spoken) {
      speak(languagePicker.value === "hi-IN"
        ? `कमांड पूरा नहीं हो सका। ${error.message}`
        : error.message);
    }
  } finally {
    processing = false;
  }
}

async function handleTranscript(transcript) {
  if (!listening || processing) {
    return;
  }
  if (isStopCommand(transcript)) {
    stopListening("Listening stopped. Click the microphone when you want me again.");
    return;
  }

  if (Date.now() < awaitingCommandUntil) {
    awaitingCommandUntil = 0;
    await executeCommand(transcript);
    return;
  }

  const command = removeWakeWord(transcript);
  if (command === null) {
    return;
  }
  const cleanedCommand = command.replace(/^[\s,.:;!?-]+/, "").trim();
  document.body.classList.add("is-awake");
  if (cleanedCommand) {
    await executeCommand(cleanedCommand);
  } else {
    awaitingCommandUntil = Date.now() + 8000;
    setStatus("I'm listening…");
    speak(languagePicker.value === "hi-IN" ? "जी, बताइए।" : "I'm listening.");
    window.setTimeout(() => {
      if (Date.now() >= awaitingCommandUntil) {
        awaitingCommandUntil = 0;
        document.body.classList.remove("is-awake");
        if (listening) {
          setStatus("Listening for Hey Jarvis");
        }
      }
    }, 8100);
  }
}

function setupRecognition() {
  if (!SpeechRecognition) {
    setStatus("Speech recognition is unavailable; typed commands still work.");
    return;
  }
  recognition = new SpeechRecognition();
  recognition.continuous = true;
  recognition.interimResults = true;
  recognition.maxAlternatives = 1;
  recognition.lang = languagePicker.value;

  recognition.onresult = (event) => {
    let transcript = "";
    for (let index = event.resultIndex; index < event.results.length; index += 1) {
      const result = event.results[index];
      if (result.isFinal) {
        transcript += `${result[0].transcript} `;
      }
    }
    if (transcript.trim()) {
      void handleTranscript(transcript.trim());
    }
  };
  recognition.onerror = (event) => {
    if (event.error === "no-speech" || event.error === "aborted") {
      return;
    }
    const message = event.error === "not-allowed" || event.error === "service-not-allowed"
      ? "Microphone permission was denied."
      : event.error === "network"
        ? "Speech service is unavailable. Check your internet connection."
        : `Speech recognition error: ${event.error}`;
    setStatus(message);
    if (event.error === "not-allowed" || event.error === "service-not-allowed" || event.error === "network") {
      setListening(false);
    }
  };
  recognition.onend = () => {
    if (listening) {
      restartTimer = window.setTimeout(() => {
        if (listening) {
          try {
            recognition.start();
          } catch {
            setStatus("Reconnecting to speech recognition…");
          }
        }
      }, 350);
    }
  };
}

microphoneButton.addEventListener("click", () => {
  if (listening) {
    stopListening();
  } else {
    startListening();
  }
});

languagePicker.addEventListener("change", () => {
  const restart = listening;
  if (restart) {
    stopListening();
  }
  if (recognition) {
    recognition.lang = languagePicker.value;
  }
  if (restart) {
    startListening();
  }
});

commandForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void executeCommand(commandInput.value, false);
  commandInput.value = "";
});

document.querySelectorAll("[data-command]").forEach((button) => {
  button.addEventListener("click", () => {
    void executeCommand(button.dataset.command, false);
  });
});

document.querySelector("#clear-button").addEventListener("click", () => {
  conversation.replaceChildren();
  const emptyState = document.createElement("p");
  emptyState.className = "empty-state";
  emptyState.textContent = "Your recent commands and JARVIS responses will appear here.";
  conversation.append(emptyState);
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

if (SpeechRecognition) {
  microphoneButton.disabled = false;
}
setupRecognition();
window.jarvis.getWorkspace().then((workspace) => {
  workspacePath.textContent = workspace;
}).catch((error) => {
  setStatus(`Could not load workspace: ${error.message}`);
});
