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

The installer installs the CPU-optimized `whisper-cli.exe` and multilingual
base model to `%APPDATA%\JARVIS\voice`. If a local Windows bundle is present in
the ignored repository `whisper\Release` and `whisper\models` folders, it uses
that bundle; otherwise it builds the pinned official whisper.cpp v1.9.6 source.
The source-build path requires CMake and Visual Studio Build Tools with the
**Desktop development with C++** workload.

```powershell
Set-Location electron
.\install-whisper.ps1
```

If PowerShell blocks the script, run it with a process-scoped policy override:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-whisper.ps1
```

The script is repeatable; use `.\install-whisper.ps1 -Force` to rebuild from
source and redownload. It uses four CPU build workers and a four-thread transcription
default to leave resources available for Ollama. Override the paths or thread
count with `JARVIS_WHISPER_PATH`, `JARVIS_WHISPER_MODEL`, and
`JARVIS_WHISPER_THREADS` in the repository-root `.env` file (see
[`.env.example`](../.env.example)). The multilingual `ggml-base.bin` supports
English and Hindi; Hinglish uses Whisper's automatic language detection. JARVIS
speaks through Windows SAPI. Install a Hindi speech voice in Windows language
settings if needed; set `JARVIS_SAPI_VOICE` to an exact voice name to pin one.

Install Ollama and its smaller tool-capable model for a 16 GB RAM / 4 GB VRAM
system:

```powershell
winget install --id Ollama.Ollama --exact
ollama pull qwen2.5:3b
```

Ollama should be running locally at `http://127.0.0.1:11434`; if its tray
application is not running, start it with `ollama serve`. Ollama chooses GPU
offload based on available VRAM. Avoid loading other models concurrently when
memory is constrained.

Select English, Hindi, or Hinglish, grant microphone access, and leave listening
enabled. Say **“Hey Jarvis”** or **“हे जार्विस”** to start a conversation
session; natural follow-ups do not need another wake phrase. The session returns
to standby after 45 seconds without speech. JARVIS resumes listening after its
spoken response unless listening is paused or the microphone is muted. Say
**“stop listening”** or
**“सुनना बंद करो”** to pause. The
dashboard can be hidden while the tray and microphone remain active. JARVIS
uses local voice activity detection and local Whisper transcription; it does
not use Chromium's online speech recognition. Audio segments are held in memory
and transient WAV files are removed after transcription; raw recordings are not
retained by default. Offline transcription requires the configured executable
and model. Microphone and SAPI behavior still depends on Windows privacy
settings and installed voices. While speaking, JARVIS stops playback after
detecting 180 ms of sustained microphone speech (with browser echo cancellation
enabled), allowing you to interrupt without waiting for transcription to finish.
Natural-language answers stream into the dashboard; speech playback starts when
the completed answer is available, so unverified partial tool responses are not
spoken.

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
Ollama and download the configured tool-capable model `qwen2.5:3b`, then
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

## Automated and live voice tests

Run the automated suites from the repository root:

```powershell
Set-Location electron
npm test
Set-Location ..\backend
go test ./...
```

For a live test, make sure Whisper and Ollama are installed and running, then:

```powershell
Set-Location electron
npm start
```

In JARVIS, grant microphone access, select English, and say **“Hey Jarvis, tell
me a short greeting.”** Wait for the spoken reply, then ask a follow-up without
the wake phrase. Repeat with Hindi and Hinglish selected. While a response is
being spoken, begin speaking to test interruption; also test silence returning
the assistant to standby, and pause/mute from the dashboard or tray. These
device-dependent checks must be completed on the target Windows laptop; passing
automated tests alone does not verify microphone, echo cancellation, SAPI voice,
or real-time behavior.

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
