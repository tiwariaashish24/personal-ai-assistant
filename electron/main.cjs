const { app, BrowserWindow, dialog, ipcMain, session } = require("electron");
const { spawn } = require("node:child_process");
const readline = require("node:readline");
const fs = require("node:fs/promises");
const path = require("node:path");

const backendDirectory = path.resolve(__dirname, "..", "backend");
let mainWindow;
let workspaceDirectory;
let assistantProcess;
let pendingRequests = [];

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
    }
  });
  child.on("close", (code) => {
    if (assistantProcess === child) {
      assistantProcess = undefined;
      rejectPendingRequests(`The Go assistant stopped${code === null ? "" : ` (exit ${code})`}.`);
    }
  });
  return child;
}

function executeCommand(command) {
  if (typeof command !== "string" || command.trim() === "" || command.length > 4096) {
    return Promise.reject(new Error("Enter a command up to 4096 characters long."));
  }

  const child = startAssistant();
  return new Promise((resolve, reject) => {
    pendingRequests.push({ resolve, reject });
    child.stdin.write(`${JSON.stringify({ command })}\n`, (error) => {
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
  ipcMain.handle("jarvis:workspace", (event) => {
    if (!isTrustedWindow(event)) {
      throw new Error("Untrusted workspace request.");
    }
    return workspaceDirectory;
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
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1000,
    height: 760,
    minWidth: 360,
    minHeight: 600,
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
  mainWindow.on("closed", () => {
    mainWindow = undefined;
  });
}

app.whenReady().then(async () => {
  app.setName("JARVIS");
  workspaceDirectory = path.join(app.getPath("documents"), "JARVIS");
  await fs.mkdir(workspaceDirectory, { recursive: true });

  session.defaultSession.setPermissionRequestHandler((webContents, permission, callback) => {
    callback(permission === "media" && mainWindow !== undefined && webContents === mainWindow.webContents);
  });
  session.defaultSession.setPermissionCheckHandler((webContents, permission) => {
    return permission === "media" && mainWindow !== undefined && webContents === mainWindow.webContents;
  });

  registerIPC();
  createWindow();
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("before-quit", () => {
  rejectPendingRequests("JARVIS is closing.");
  if (assistantProcess) {
    assistantProcess.kill();
    assistantProcess = undefined;
  }
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
