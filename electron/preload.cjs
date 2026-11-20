const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("jarvis", {
  executeCommand: (command) => ipcRenderer.invoke("jarvis:execute", command),
  getWorkspace: () => ipcRenderer.invoke("jarvis:workspace"),
  chooseWorkspace: () => ipcRenderer.invoke("jarvis:choose-workspace"),
});
