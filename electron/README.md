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
Ollama, download a tool-capable model such as `qwen2.5:3b`, and start the Ollama
service. JARVIS connects to its OpenAI-compatible API at
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
connected Google accounts. AI cannot run shell commands or delete files. A React portfolio starter can be scaffolded inside
the workspace and opened directly in VS Code; run `npm install` in that folder
before starting its Vite development server.

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
