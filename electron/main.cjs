const { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, session, Tray } = require("electron");
const { spawn } = require("node:child_process");
const readline = require("node:readline");
const fs = require("node:fs/promises");
const path = require("node:path");
const { VoiceRuntime } = require("./voice-runtime.cjs");

const backendDirectory = path.resolve(__dirname, "..", "backend");
let mainWindow;
let workspaceDirectory;
let assistantProcess;
let pendingRequests = [];
let tray;
let voiceRuntime;
let voiceAvailability = { available: false, message: "Voice runtime is not initialized." };
let voicePreferences = { startAtLogin: true, listeningEnabled: true, muted: false };
let voiceState = "Offline";
let voiceDetail = "";
let restartTimer;
let restartDelay = 1000;
let isQuitting = false;
const singleInstanceLock = app.requestSingleInstanceLock();

if (!singleInstanceLock) {
  app.quit();
} else {
  app.setName("JARVIS");
}

function setVoiceState(state, detail = "") {
  voiceState = state;
  voiceDetail = detail;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send("jarvis:voice-state", { state, detail });
  }
  updateTrayMenu();
}

function updateTrayMenu() {
  if (!tray) {
    return;
  }
  const menu = Menu.buildFromTemplate([
    { label: `JARVIS — ${voiceState}`, enabled: false },
    ...(voiceDetail ? [{ label: voiceDetail, enabled: false }] : []),
    { type: "separator" },
    { label: "Open dashboard", click: showDashboard },
    {
      label: voicePreferences.listeningEnabled ? "Pause listening" : "Resume listening",
      click: () => toggleVoicePreference(voicePreferences.listeningEnabled ? "pause" : "resume"),
    },
    {
      label: voicePreferences.muted ? "Unmute microphone" : "Mute microphone",
      click: () => toggleVoicePreference(voicePreferences.muted ? "unmute" : "mute"),
    },
    {
      label: "Start at login",
      type: "checkbox",
      checked: voicePreferences.startAtLogin,
      click: (item) => {
        try {
          updateLoginStartup(item.checked);
        } catch (error) {
          setVoiceState("Error", `Could not update login startup: ${error.message}`);
          return;
        }
        voicePreferences.startAtLogin = item.checked;
        void saveVoicePreferences();
        updateTrayMenu();
      },
    },
    { type: "separator" },
    { label: "Exit JARVIS", click: () => {
      isQuitting = true;
      app.quit();
    } },
  ]);
  tray.setContextMenu(menu);
  tray.setToolTip(`JARVIS — ${voiceState}`);
}

function updateLoginStartup(openAtLogin) {
  const args = ["--background"];
  if (!app.isPackaged) {
    args.unshift(app.getAppPath());
  }
  app.setLoginItemSettings({
    openAtLogin,
    path: process.execPath,
    args,
  });
}

function createTray() {
  const image = nativeImage.createFromDataURL(
    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAO0lEQVR4nGNgoDYQX3D2Pz5MkWa8hhCrGash2BSgA7yGjBqAagBMMUUGYAO0NYDihESVpEysIXg1kwMA/03mkTBDn5AAAAAASUVORK5CYII=",
  );
  tray = new Tray(image);
  tray.on("click", showDashboard);
  updateTrayMenu();
}

function showDashboard() {
  if (!mainWindow || mainWindow.isDestroyed()) {
    createWindow();
  }
  mainWindow.show();
  mainWindow.focus();
}

function sendVoiceAction(action) {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send("jarvis:voice-action", action);
  }
}

async function toggleVoicePreference(action) {
  try {
    await setVoicePreference(action);
    sendVoiceAction(action);
  } catch (error) {
    setVoiceState("Error", `Could not update microphone setting: ${error.message}`);
  }
}

function preferencesPath() {
  return path.join(app.getPath("userData"), "preferences.json");
}

async function loadVoicePreferences() {
  try {
    const saved = JSON.parse(await fs.readFile(preferencesPath(), "utf8"));
    voicePreferences = {
      startAtLogin: saved.startAtLogin !== false,
      listeningEnabled: saved.listeningEnabled !== false,
      muted: saved.muted === true,
    };
  } catch (error) {
    if (error.code !== "ENOENT") {
      console.error(`Could not load JARVIS preferences: ${error.message}`);
    }
  }
}

async function saveVoicePreferences() {
  try {
    await fs.mkdir(app.getPath("userData"), { recursive: true });
    await fs.writeFile(preferencesPath(), JSON.stringify(voicePreferences, null, 2), { mode: 0o600 });
  } catch (error) {
    console.error(`Could not save JARVIS preferences: ${error.message}`);
  }
  updateTrayMenu();
}

function setVoicePreference(action) {
  switch (action) {
    case "resume":
      voicePreferences.listeningEnabled = true;
      break;
    case "pause":
      voicePreferences.listeningEnabled = false;
      break;
    case "mute":
      voicePreferences.muted = true;
      break;
    case "unmute":
      voicePreferences.muted = false;
      break;
    default:
      throw new Error("Unsupported microphone action.");
  }
  return saveVoicePreferences();
}

function checkTrustedWindow(event) {
  if (!mainWindow || event.sender !== mainWindow.webContents) {
    throw new Error("Untrusted voice control request.");
  }
}

async function transcribeAudio(event, audio, language) {
  checkTrustedWindow(event);
  try {
    const transcript = await voiceRuntime.transcribe(audio, language);
    if (!transcript) {
      return "";
    }
    return transcript;
  } catch (error) {
    setVoiceState("Error", error.message);
    throw error;
  }
}

async function speakText(event, text) {
  checkTrustedWindow(event);
  try {
    await voiceRuntime.speak(text);
  } catch (error) {
    if (!voiceRuntime.speechProcess) {
      setVoiceState("Error", error.message);
    }
    throw error;
  }
}

function scheduleAssistantRestart() {
  if (isQuitting || restartTimer) {
    return;
  }
  const delay = restartDelay;
  restartDelay = Math.min(restartDelay * 2, 30000);
  restartTimer = setTimeout(() => {
    restartTimer = undefined;
    if (!isQuitting && !assistantProcess) {
      startAssistant();
    }
  }, delay);
}

function rejectPendingRequests(message) {
  const pending = pendingRequests;
  pendingRequests = [];
  for (const request of pending) {
    request.reject(new Error(message));
  }
}

function startAssistant() {
  if (assistantProcess) {
    return assistantProcess;
  }
  if (restartTimer) {
    clearTimeout(restartTimer);
    restartTimer = undefined;
  }

  const child = spawn(
    "go",
    ["run", "./cmd/jarvis", "-stdio", "-workspace", workspaceDirectory],
    {
      cwd: backendDirectory,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    },
  );
  assistantProcess = child;

  const output = readline.createInterface({ input: child.stdout });
  output.on("line", (line) => {
    const request = pendingRequests.shift();
    if (!request) {
      console.error("Received an unexpected response from the assistant process.");
      return;
    }

    try {
      const response = JSON.parse(line);
      if (response.error) {
        request.reject(new Error(response.error));
      } else {
        request.resolve(response);
      }
    } catch (error) {
      request.reject(new Error(`Invalid assistant response: ${error.message}`));
    }
  });

  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (message) => {
    console.error(`[JARVIS backend] ${message.trimEnd()}`);
  });
  child.on("error", (error) => {
    if (assistantProcess === child) {
      assistantProcess = undefined;
      rejectPendingRequests(`Could not start the Go assistant: ${error.message}`);
      setVoiceState("Error", `Go assistant failed to start: ${error.message}`);
      scheduleAssistantRestart();
    }
  });
  child.on("close", (code) => {
    if (assistantProcess === child) {
      assistantProcess = undefined;
      rejectPendingRequests(`The Go assistant stopped${code === null ? "" : ` (exit ${code})`}.`);
      if (!isQuitting) {
        setVoiceState("Error", "Go assistant stopped; restarting in the background.");
        scheduleAssistantRestart();
      }
    }
  });
  child.once("spawn", () => {
    restartDelay = 1000;
  });
  return child;
}

function sendAssistantRequest(request) {
  const child = startAssistant();
  return new Promise((resolve, reject) => {
    pendingRequests.push({ resolve, reject });
    child.stdin.write(`${JSON.stringify(request)}\n`, (error) => {
      if (!error) {
        return;
      }
      const index = pendingRequests.findIndex((request) => request.reject === reject);
      if (index !== -1) {
        pendingRequests.splice(index, 1);
      }
      reject(new Error(`Could not send command to the Go assistant: ${error.message}`));
    });
  });
}

async function executeCommand(command) {
  if (typeof command !== "string" || command.trim() === "" || command.length > 4096) {
    throw new Error("Enter a command up to 4096 characters long.");
  }
  const result = await sendAssistantRequest({ command });
  if (!result.confirmation) {
    return result;
  }
  const confirmation = await dialog.showMessageBox(mainWindow, {
    type: "warning",
    title: "Confirm JARVIS action",
    message: result.confirmation,
    detail: "JARVIS will only perform the action described above after you approve it.",
    buttons: ["Cancel", "Confirm"],
    defaultId: 0,
    cancelId: 0,
    noLink: true,
  });
  if (confirmation.response !== 1) {
    await sendAssistantRequest({ command, cancelConfirmation: true });
    return { output: "Cancelled. No changes were made." };
  }
  return sendAssistantRequest({ command, confirmed: true });
}

function isTrustedWindow(event) {
  return mainWindow && event.sender === mainWindow.webContents;
}

function registerIPC() {
  ipcMain.handle("jarvis:execute", (event, command) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted command request.");
    }
    return executeCommand(command);
  });
  ipcMain.handle("jarvis:clear-conversation", (event) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted conversation request.");
    }
    return sendAssistantRequest({ clearConversation: true });
  });
  ipcMain.handle("jarvis:workspace", (event) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted workspace request.");
    }
    return workspaceDirectory;
  });
  ipcMain.handle("jarvis:integrations", async (event) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted integrations request.");
    }
    return sendAssistantRequest({ command: "integrations" });
  });
  ipcMain.handle("jarvis:choose-workspace", async (event) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted workspace request.");
    }
    const result = await dialog.showOpenDialog(mainWindow, {
      title: "Choose JARVIS workspace",
      properties: ["openDirectory", "createDirectory"],
    });
    if (result.canceled || result.filePaths.length === 0) {
      return workspaceDirectory;
    }

    workspaceDirectory = result.filePaths[0];
    if (assistantProcess) {
      const previousProcess = assistantProcess;
      assistantProcess = undefined;
      rejectPendingRequests("The workspace changed. Please retry the command.");
      previousProcess.kill();
    }
    return workspaceDirectory;
  });
  ipcMain.handle("jarvis:voice-settings", async (event) => {
    checkTrustedWindow(event);
    return {
      ...voicePreferences,
      state: voiceState,
      detail: voiceDetail,
      runtime: voiceAvailability,
    };
  });
  ipcMain.handle("jarvis:voice-control", async (event, action) => {
    checkTrustedWindow(event);
    await setVoicePreference(action);
    sendVoiceAction(action);
    return { ...voicePreferences };
  });
  ipcMain.handle("jarvis:voice-transcribe", transcribeAudio);
  ipcMain.handle("jarvis:voice-speak", speakText);
  ipcMain.handle("jarvis:voice-interrupt", (event) => {
    checkTrustedWindow(event);
    return voiceRuntime.interrupt();
  });
  ipcMain.handle("jarvis:tasks", async (event) => {
    checkTrustedWindow(event);
    return sendAssistantRequest({ command: "tasks" });
  });
  ipcMain.on("jarvis:voice-state", (event, update) => {
    if (!mainWindow || event.sender !== mainWindow.webContents ||
        !update || typeof update.state !== "string") {
      return;
    }
    const allowed = new Set([
      "Standby", "Listening", "Thinking", "Speaking", "Working in background",
      "Sleep", "Offline", "Error",
    ]);
    if (allowed.has(update.state)) {
      setVoiceState(update.state, String(update.detail || ""));
    }
  });
}

function createWindow() {
  const startHidden = process.argv.includes("--background");
  mainWindow = new BrowserWindow({
    width: 1000,
    height: 760,
    minWidth: 360,
    minHeight: 600,
    show: false,
    backgroundColor: "#080d19",
    title: "JARVIS",
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      backgroundThrottling: false,
      preload: path.join(__dirname, "preload.cjs"),
    },
  });

  mainWindow.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  mainWindow.loadFile(path.join(__dirname, "index.html"));
  mainWindow.once("ready-to-show", () => {
    if (!startHidden) {
      mainWindow.show();
    }
  });
  mainWindow.on("close", (event) => {
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });
  mainWindow.on("show", () => {
    mainWindow.webContents.send("jarvis:voice-state", { state: voiceState, detail: voiceDetail });
  });
  mainWindow.on("closed", () => {
    mainWindow = undefined;
  });
}

if (singleInstanceLock) {
  app.on("second-instance", () => showDashboard());
  app.whenReady().then(async () => {
    await loadVoicePreferences();
    const voiceDirectory = path.join(app.getPath("userData"), "voice");
    voiceRuntime = new VoiceRuntime({
      voiceDirectory,
      onState: (state) => {
        const nextState = state === "Speaking"
          ? state
          : !voiceAvailability.available ? "Offline"
            : voicePreferences.listeningEnabled && !voicePreferences.muted ? "Listening" : "Sleep";
        setVoiceState(nextState);
      },
    });
    await voiceRuntime.initialize([
      path.resolve(__dirname, "..", ".env"),
      path.join(__dirname, ".env"),
    ]);
    voiceAvailability = await voiceRuntime.availability();
    if (!voiceAvailability.available) {
      setVoiceState("Offline", voiceAvailability.message);
    }

    if (process.platform === "win32") {
      try {
        updateLoginStartup(voicePreferences.startAtLogin);
      } catch (error) {
        console.error(`Could not configure Windows login startup: ${error.message}`);
        setVoiceState("Error", `Login startup could not be configured: ${error.message}`);
      }
    }
    await saveVoicePreferences();
    workspaceDirectory = path.join(app.getPath("documents"), "JARVIS");
    await fs.mkdir(workspaceDirectory, { recursive: true });

    session.defaultSession.setPermissionRequestHandler((webContents, permission, callback) => {
      callback(permission === "media" && mainWindow !== undefined && webContents === mainWindow.webContents);
    });
    session.defaultSession.setPermissionCheckHandler((webContents, permission) => {
      return permission === "media" && mainWindow !== undefined && webContents === mainWindow.webContents;
    });

    registerIPC();
    createTray();
    createWindow();
    startAssistant();
    app.on("activate", showDashboard);
  }).catch((error) => {
    setVoiceState("Error", `JARVIS could not initialize: ${error.message}`);
    dialog.showErrorBox("JARVIS startup failed", error.message);
  });

  app.on("before-quit", () => {
    isQuitting = true;
    if (restartTimer) {
      clearTimeout(restartTimer);
      restartTimer = undefined;
    }
    rejectPendingRequests("JARVIS is exiting.");
    if (voiceRuntime) {
      voiceRuntime.close();
    }
    if (assistantProcess) {
      assistantProcess.kill();
      assistantProcess = undefined;
    }
    if (tray) {
      tray.destroy();
      tray = undefined;
    }
  });

  app.on("window-all-closed", () => {});
}
