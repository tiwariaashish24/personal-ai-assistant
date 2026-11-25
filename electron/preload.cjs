const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("jarvis", {
  executeCommand: (command) => ipcRenderer.invoke("jarvis:execute", command),
  clearConversation: () => ipcRenderer.invoke("jarvis:clear-conversation"),
  getWorkspace: () => ipcRenderer.invoke("jarvis:workspace"),
  chooseWorkspace: () => ipcRenderer.invoke("jarvis:choose-workspace"),
});
