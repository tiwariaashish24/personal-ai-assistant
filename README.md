# JARVIS Assistant

JARVIS is a local-first desktop assistant for Windows with a Go command engine,
an Electron dashboard, offline voice transcription, speech output, and optional
AI/OpenAI-compatible integrations.

The assistant is designed to keep destructive or external actions behind
confirmation and persistent permission rules. It does not expose arbitrary
shell command execution.

## Features

- Interactive CLI and JSON-lines process mode for desktop clients.
- Electron desktop dashboard with workspace selection, connections, activity,
  background tasks, microphone controls, and system tray support.
- Wake-word voice sessions in English, Hindi, and Hinglish.
- Offline Whisper transcription using `whisper-cli` and a multilingual model.
- Windows SAPI speech responses with interruption support.
- Natural-language commands through Ollama by default, or OpenAI/OpenAI-compatible
  APIs when configured.
- Safe application launching for Chrome, VS Code, and Notepad.
- Workspace-safe file read, create, append, and recoverable delete operations.
- Recoverable trash and restoration for deleted workspace files.
- Read-only permission rules for approved folders.
- Background tasks, reminders, cancellable work, and in-memory task history.
- Google OAuth integrations for Gmail and Google Calendar, including search,
  read, send/reply/forward/archive/trash, attachment downloads, event listing,
  and event management.
- Persistent integration policies, per-recipient email rules, activity audit
  entries, and communication emergency stop.
- SQLite storage for permissions, accounts, and audit metadata.
- OS credential vault storage for OAuth tokens.

## Repository layout

```text
backend/       Go command engine, integrations, permissions, tasks, and CLI
electron/      Electron desktop app, renderer UI, voice runtime, Whisper installer
.env.example   Environment variable template
whisper/       Local Whisper binaries/models used by the installer, ignored by git
```

`dashboard/` and `node-service/` are reserved project directories and currently
contain no implemented application code.

## Requirements

- Go 1.24 or newer for the backend.
- Node.js and npm for the Electron app.
- Windows for the complete desktop/voice workflow.
- Ollama and a tool-capable model for default natural-language commands.
- Whisper.cpp `whisper-cli.exe` plus a multilingual model for offline voice.
- Google Cloud OAuth credentials for Gmail/Calendar integrations.

## Setup

Clone the repository and copy the environment template:

```powershell
copy .env.example .env
```

Do not commit real OAuth credentials or API keys.

### Backend

Run the interactive assistant:

```powershell
cd backend
go run ./cmd/jarvis
```

Choose a workspace explicitly when needed:

```powershell
go run ./cmd/jarvis -workspace "C:\path\to\workspace"
```

The backend also supports JSON-lines mode for the Electron app:

```powershell
go run ./cmd/jarvis -stdio
```

### Desktop app

```powershell
cd electron
npm install
npm start
```

The app starts the Go assistant as a private child process and does not expose a
network listener. By default, file commands use the `JARVIS` folder under the
Windows Documents directory. Use the Workspace control to select another folder.

## Voice setup

Install the local Whisper runtime and model:

```powershell
cd electron
.\install-whisper.ps1
```

If PowerShell blocks the script:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-whisper.ps1
```

The installer uses `%APPDATA%\JARVIS\voice` by default and can rebuild the pinned
whisper.cpp source when no local bundle is present. Configure paths and CPU
threads through `.env`:

```text
JARVIS_WHISPER_PATH=
JARVIS_WHISPER_MODEL=
JARVIS_WHISPER_THREADS=4
JARVIS_SAPI_VOICE=
```

For natural-language commands, install Ollama and a tool-capable model:

```powershell
winget install --id Ollama.Ollama --exact
ollama pull qwen2.5:3b
ollama serve
```

JARVIS uses Ollama's OpenAI-compatible endpoint at
`http://127.0.0.1:11434/v1` by default.

## AI provider configuration

Set one of these provider modes:

```text
JARVIS_AI_PROVIDER=ollama
```

```text
JARVIS_AI_PROVIDER=openai
OPENAI_API_KEY=your-api-key
JARVIS_AI_MODEL=gpt-4o-mini
```

Optional settings include `JARVIS_AI_BASE_URL` and `JARVIS_AI_MODEL`.
Conversation history is held in memory for the process lifetime and can be
cleared from the desktop app.

AI actions are restricted to supported application launches, workspace-safe file
operations, system information, Google integrations, and portfolio scaffolding.
AI cannot run arbitrary shell commands or delete files.

## Google integrations

To enable Gmail and Google Calendar:

1. Create a Google Cloud project.
2. Enable the Gmail API and Google Calendar API.
3. Configure the OAuth consent screen.
4. Create an OAuth client ID for a Desktop app.
5. Add test users while the consent screen is in testing.
6. Set `JARVIS_GOOGLE_CLIENT_ID` in `.env`.
7. Restart JARVIS, run `configure google`, then `connect google`.

Desktop OAuth uses a temporary loopback callback on `127.0.0.1`. Tokens and
refresh tokens are stored in the OS credential vault; SQLite stores account
metadata, permissions, and audit data.

Useful commands include:

```text
integrations
test google <email>
configure google
disconnect google <email>
integration permissions
integration permission <provider> <account|*> <action> <recipient|*> <allow|confirm|deny>
integration activity
integration emergency-stop on
integration emergency-stop off
```

Sensitive operations such as sending email to unknown recipients, modifying
messages or calendar events, downloading attachments, trashing mail, and
deleting events require confirmation unless an explicit persistent policy
allows them.

## Backend commands

The interactive assistant supports commands including:

- `help`
- `open`, `create`, `read`, `append`, `delete`
- `system`
- `permissions`, `permission`
- `trash`, `restore <id>`
- `integrations`, `connect`, `disconnect`, `configure`, `test`, `integration`
- `tasks`, `cancel`, `remind`
- `exit`

Some supported Hindi commands include:

- `क्रोम खोलो`
- `सिस्टम जानकारी`
- `फाइल बनाओ <नाम> <टेक्स्ट>`
- `फाइल पढ़ो <नाम>`

Writes remain inside the selected workspace. Reading outside the workspace
requires explicit read-only folder approval. File deletion always requires
confirmation and moves files to recoverable workspace trash.

## Security model

- No arbitrary shell execution.
- Application launching is limited to an allowlist.
- File writes stay inside the selected workspace.
- External folders can only receive read-only permission.
- File deletion and sensitive Google operations require confirmation.
- Unknown email recipients require confirmation.
- Persistent per-recipient email permissions can allow trusted contacts.
- OAuth tokens are stored in the OS credential vault, not SQLite.
- Google communication can be blocked with the integration emergency stop.
- Attachment downloads are limited to 25 MiB and remain workspace-bound.

## Testing

Run the backend tests:

```powershell
cd backend
go test ./...
```

Run the Electron voice tests:

```powershell
cd electron
npm test
```

For a live voice test, start the desktop app after Whisper and Ollama are
installed and running, grant microphone access, select English, Hindi, or
Hinglish, and say **“Hey Jarvis”** or **“हे जार्विस”**.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
