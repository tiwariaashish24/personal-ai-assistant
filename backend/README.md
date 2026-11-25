# JARVIS command assistant

Run the interactive assistant from this directory:

```powershell
go run ./cmd/jarvis
```

Use `-workspace <directory>` to choose where file commands may operate. The
assistant supports `help`, `open`, `create`, `read`, `append`, `delete`,
`system`, `permissions`, `permission`, `trash`, `restore`, `integrations`,
`connect`, `disconnect`, `configure`, `test`, `integration`, and `exit`. Writes
stay inside the workspace; reading another folder requires explicit approval.
Application launching is limited to Chrome, VS Code, and Notepad; arbitrary
shell commands are not supported.

The Electron desktop app connects over the assistant's `-stdio` JSON-lines mode.
Some common Hindi commands are supported, including `क्रोम खोलो`,
`सिस्टम जानकारी`, `फाइल बनाओ <नाम> <टेक्स्ट>`, and `फाइल पढ़ो <नाम>`.

Run the engine tests with `go test ./...`.

Natural-language requests are sent to Ollama by default using its
OpenAI-compatible endpoint at `http://127.0.0.1:11434/v1`; install Ollama and a
tool-capable model such as `qwen2.5:3b` to enable AI. To use OpenAI, set
`JARVIS_AI_PROVIDER=openai` and `OPENAI_API_KEY`. Optional settings:
`JARVIS_AI_BASE_URL` and `JARVIS_AI_MODEL`. These can be set as environment
variables or in a `.env` file in the repository root or this directory.

AI chat history is held in memory for the process lifetime and can be cleared
through the desktop app. Function calls are restricted to the supported
applications, workspace-safe file reads/creates/appends, system information,
and actions on explicitly connected Google accounts; arbitrary shell commands
and AI file deletion are not supported.
The AI can also scaffold a React/Vite portfolio project inside the workspace
and open that folder in VS Code; dependencies are installed separately with
`npm install`.
After Google OAuth connection, it can search/read Gmail, send/reply/forward/
archive/trash messages, download attachments into the workspace, and manage
Google Calendar events. Sending to untrusted recipients and changes to messages
or events require confirmation by default. Moving messages to Trash and deleting
calendar events always require confirmation; downloaded attachments are limited
to 25 MiB.

Permission policies persist in SQLite under the OS user configuration directory
in `JARVIS/jarvis.db`; set `JARVIS_DB_PATH` to override it. The selected
workspace is approved for reading, creating, and appending files. Add or revoke
read-only folder access with
`permission read add "<absolute folder>"` and
`permission read remove "<absolute folder>"`; inspect policies with
`permissions`. File deletion always requires confirmation and moves files to
recoverable trash; use `trash` and `restore <id>` to recover them.

## Google integrations

Copy the root `.env.example` to `.env`, then set `JARVIS_GOOGLE_CLIENT_ID`.
Create a Google Cloud project, enable Gmail API and Google Calendar API, set up
the OAuth consent screen, and create an OAuth client ID for a **Desktop app**.
Add your Google account as a test user while the consent screen is in testing.
Desktop OAuth uses a temporary loopback callback on `127.0.0.1`; do not add a
public redirect URL. Restart JARVIS, run `configure google`, then `connect
google` and approve the requested account scopes.

Requested scopes:

- `openid` and `userinfo.email` identify the connected account.
- `gmail.modify` reads, searches, archives, and trashes mail.
- `gmail.send` sends, replies to, and forwards messages.
- `calendar.events` lists and manages events on the primary calendar.

Google can require verification for Gmail scopes before accounts outside the
configured test-user list can connect. Tokens and refresh tokens are stored in
the operating system credential vault (Windows Credential Manager, macOS
Keychain, or the Linux Secret Service); SQLite stores account metadata,
permissions, and an audit trail, not OAuth tokens. Automatic refresh persists
rotated tokens back to the credential vault. `disconnect google <email>`
removes JARVIS's local token and permissions; it does not revoke the OAuth grant
at Google, which can separately be removed from the Google Account security
page.

Use `integrations`, `test google <email>`, and `configure google` to manage the
connection. `integration permission <provider> <account|*> <action>
<recipient|*> <allow|confirm|deny>` manages persistent policies; for example,
trust a contact with
`integration permission google me@example.com send_email friend@example.com allow`.
Unknown email recipients require confirmation. Wildcard permissions do not
silently trust outbound email recipients. `integration activity` lists recent
operations without recording message bodies. Use `integration emergency-stop on`
to block Google communication APIs immediately; `integration emergency-stop off`
requires confirmation to resume.
Attachment downloads require confirmation by default; an account-scoped
`download_attachment` allow policy enables automatic downloads within the 25 MiB
limit and selected workspace.

Outlook, Telegram, Discord, WhatsApp, Meta, LinkedIn, X, and SMS are not
implemented in this phase and are not simulated. Email forwarding currently
forwards message text and headers; provider attachments are not copied.
Transfers still require mandatory authentication and a payment integration;
permanent file deletion is not exposed.
