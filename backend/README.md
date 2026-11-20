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
