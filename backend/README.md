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

Run the engine tests with `go test ./...`.
