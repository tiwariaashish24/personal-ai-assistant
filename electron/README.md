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
Closing the dashboard hides it to the system tray; choose **Exit JARVIS** from
the tray menu to stop the assistant. The tray also controls microphone mute,
listening pause, and login startup. JARVIS is configured to start at Windows
login by default.

## Voice

Install whisper.cpp for Windows and a multilingual model, then copy them into
`%APPDATA%\JARVIS\voice` as `whisper-cli.exe` and `ggml-small.bin`. Alternatively,
set `JARVIS_WHISPER_PATH` and `JARVIS_WHISPER_MODEL` in the repository-root
`.env` file to their full paths (see `.env.example`). Use the official
whisper.cpp Windows release and multilingual Whisper model; Hindi requires a
multilingual model, not an English-only model. On Windows, JARVIS speaks using
Windows SAPI and installed system voices. Install a Hindi speech voice in
Windows language settings if needed; set `JARVIS_SAPI_VOICE` to an exact SAPI
voice name to pin one.

1. Download a Windows release of `whisper-cli.exe` from
   [whisper.cpp releases](https://github.com/ggml-org/whisper.cpp/releases) and
   place it in `%APPDATA%\JARVIS\voice`.
2. Download the multilingual `ggml-small.bin` model from the
   [whisper.cpp model repository](https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin?download=true)
   into the same folder.
3. If you choose a different location or filename, set both `JARVIS_WHISPER_PATH`
   and `JARVIS_WHISPER_MODEL` to their full paths in the root `.env`, then
   restart JARVIS.

Select English, Hindi, or Hinglish, grant microphone access, and leave listening
enabled. Say **“Hey Jarvis”** or **“हे जार्विस”** to start a conversation
session; natural follow-ups do not need another wake phrase. The session returns
to standby after 45 seconds without speech. Say **“stop listening”** or
**“सुनना बंद करो”** to pause. The
dashboard can be hidden while the tray and microphone remain active. JARVIS
uses local voice activity detection and local Whisper transcription; it does
not use Chromium's online speech recognition. Audio segments are held in memory
and transient WAV files are removed after transcription; raw recordings are not
retained by default. Offline transcription requires the configured executable
and model. Microphone and SAPI behavior still depends on Windows privacy
settings and installed voices.

The voice layer invokes the Go assistant's existing supported commands. It does
not interpret or run arbitrary shell commands.

## Connected applications

The **Connections** panel lists configured and connected Google accounts and
provides **Connect**, **Disconnect**, **Configure**, and **Test connection**
actions. Configure a Google Desktop OAuth client by following
[backend Google integration setup](../backend/README.md#google-integrations).
OAuth happens in the system browser; JARVIS listens only on a temporary
loopback callback and stores tokens in the OS credential vault.

Once connected, use natural language to search/read Gmail and manage Google
Calendar. Sending email, forwarding/replying, archiving messages, downloading
attachments, and modifying calendar events are confirmation-gated unless a
persistent permission explicitly allows the action. Moving messages to Trash
and deleting calendar events always require confirmation. Unknown recipients
always require confirmation; multiple recipients and detected sensitive data
also require confirmation. Manage recipient permissions and review activity
from the backend with `integration permissions`, `integration permission`, and
`integration activity`. The Connections panel also provides an emergency stop;
resuming communication requires confirmation.

## AI conversation

Natural-language requests and follow-up questions use Ollama by default. Install
Ollama, download the configured tool-capable model such as `qwen2.5:7b`, and
start the Ollama service. JARVIS connects to its OpenAI-compatible API at
`http://127.0.0.1:11434/v1`.

To use OpenAI instead, set these variables before starting the desktop app (or
put them in a `.env` file in the repository root or `backend` directory):

```text
JARVIS_AI_PROVIDER=openai
OPENAI_API_KEY=your-api-key
JARVIS_AI_MODEL=gpt-4o-mini
```

Optional settings are `JARVIS_AI_PROVIDER=ollama|openai`,
`JARVIS_AI_BASE_URL`, and `JARVIS_AI_MODEL`. Keep API keys out of source control.
Conversation context stays in memory for the lifetime of the app; **Clear**
removes both the visible transcript and that context. AI actions are limited to
opening Chrome, VS Code, or Notepad; reading, creating, and appending files
inside the selected workspace; system information; and actions on explicitly
connected Google accounts. AI cannot run shell commands or delete files. A React
portfolio starter can be scaffolded inside the workspace as a cancellable
background task and opened directly in VS Code;
check **Background tasks** for completion, then run `npm install` in that folder
before starting its Vite development server.

Use `remind 10m take a break` or ask JARVIS to schedule a reminder. Background
tasks run concurrently with conversations, appear in **Background tasks**, and
can be cancelled from the dashboard or with `cancel task <id>`. Completion and
reminder notifications are spoken when the configured speech output is
available. Scheduled reminders and task history live in memory only and are
lost if JARVIS exits or Windows restarts.

## Troubleshooting

- **Voice is Offline:** check that both Whisper paths point to readable files,
  that `whisper-cli.exe` runs on this Windows version, and that the model is
  multilingual. Restart JARVIS after changing `.env`.
- **No microphone input:** allow microphone access in Windows Privacy settings
  and in the JARVIS app; check the visible mic indicator and unmute/resume from
  the dashboard or tray.
- **No spoken response:** check Windows speech settings and install an English
  or Hindi SAPI voice. Text commands and the dashboard remain available without
  speech output.
- **Tray/startup unavailable:** login startup is supported on Windows; use the
  tray's **Start at login** option and verify Windows startup-app settings.

## Permissions

Permission decisions are stored in SQLite at your OS user configuration
directory under `JARVIS/jarvis.db`; set `JARVIS_DB_PATH` to override it. The
selected workspace is approved for reads and file creation/appending, and the
supported application allowlist can always be opened. Use `permissions` to
review saved policies. Approve an additional read-only folder with
`permission read add "C:\path\to\folder"` and revoke it with
`permission read remove "C:\path\to\folder"`. External folder access is
read-only; writes remain restricted to the selected workspace.

Every file deletion asks for confirmation and moves the file into recoverable
workspace trash. Run `trash` to see items and `restore <id>` to recover one.
Gmail and Google Calendar are available after OAuth setup and account
connection. Outlook, social messaging, SMS, and payment integrations are not
implemented yet. Transfers require mandatory authentication when a payment
integration is added, and permanent deletion is not exposed.
