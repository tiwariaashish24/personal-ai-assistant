# JARVIS command assistant

Run the interactive assistant from this directory:

```powershell
go run ./cmd/jarvis
```

Use `-workspace <directory>` to choose where file commands may operate. The
assistant supports `help`, `open`, `create`, `read`, `append`, `delete`,
`system`, and `exit`. File paths must be relative to the workspace. Application
launching is limited to Chrome, VS Code, and Notepad; arbitrary shell commands
are not supported.

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
applications, workspace-safe file reads/creates/appends, and system
information; arbitrary shell commands and AI file deletion are not supported.
The AI can also scaffold a React/Vite portfolio project inside the workspace
and open that folder in VS Code; dependencies are installed separately with
`npm install`.
