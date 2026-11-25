const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("jarvis", {
  executeCommand: (command) => ipcRenderer.invoke("jarvis:execute", command),
  clearConversation: () => ipcRenderer.invoke("jarvis:clear-conversation"),
  getIntegrations: () => ipcRenderer.invoke("jarvis:integrations"),
  getTasks: () => ipcRenderer.invoke("jarvis:tasks"),
  getVoiceSettings: () => ipcRenderer.invoke("jarvis:voice-settings"),
  setVoiceControl: (action) => ipcRenderer.invoke("jarvis:voice-control", action),
  transcribeAudio: (audio, language) => ipcRenderer.invoke("jarvis:voice-transcribe", audio, language),
  speak: (text) => ipcRenderer.invoke("jarvis:voice-speak", text),
  interruptSpeech: () => ipcRenderer.invoke("jarvis:voice-interrupt"),
  setVoiceState: (state, detail) => ipcRenderer.send("jarvis:voice-state", { state, detail }),
  getWorkspace: () => ipcRenderer.invoke("jarvis:workspace"),
  chooseWorkspace: () => ipcRenderer.invoke("jarvis:choose-workspace"),
  onVoiceAction: (listener) => {
    const handler = (_event, action) => listener(action);
    ipcRenderer.on("jarvis:voice-action", handler);
    return () => ipcRenderer.removeListener("jarvis:voice-action", handler);
  },
  onVoiceState: (listener) => {
    const handler = (_event, state) => listener(state);
    ipcRenderer.on("jarvis:voice-state", handler);
    return () => ipcRenderer.removeListener("jarvis:voice-state", handler);
  },
});
