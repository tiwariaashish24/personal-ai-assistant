# JARVIS desktop voice assistant

## Run

Install Go and Node.js, then start the Electron app:

```powershell
cd electron
npm install
npm start
```

The app starts the Go assistant as a private JSON-lines child process. It does
not expose a network listener. File commands use the `JARVIS` folder in your
Documents directory by default; use **Workspace** to choose another folder.

## Voice

Choose English or Hindi, grant microphone permission, and select **Start
listening**. Say **“Hey Jarvis”** followed by a supported command, or say the
wake phrase first and give the command within eight seconds. Say **“stop
listening”** (or **“सुनना बंद करो”**) to pause the microphone.

Speech recognition uses the speech service provided by Electron/Chromium and
may require an internet connection; it is not offline, and the speech service
may process microphone audio. Text-to-speech uses installed system voices.
Availability and voice quality for each language depend on the machine's speech
services. Recognition runs only while listening is enabled and while this app
is open. Hindi commands include `क्रोम खोलो`, `नोटपैड खोलो`, `सिस्टम जानकारी`,
`फाइल बनाओ <नाम> <टेक्स्ट>`, and `फाइल पढ़ो <नाम>`.

The voice layer invokes the Go assistant's existing supported commands. It does
not interpret or run arbitrary shell commands.
