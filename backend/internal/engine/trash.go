package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	trashDirectory = ".jarvis-trash"
	trashMarker    = ".jarvis-trash-version"
	trashVersion   = "jarvis-trash-v1"
)

type trashRecord struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	DeletedAt time.Time `json:"deleted_at"`
}

func (e *Engine) moveToTrash(path string) (string, error) {
	relativePath, err := filepath.Rel(e.root, path)
	if err != nil || relativePath == "." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", errors.New("file path must stay inside the workspace")
	}
	trashRoot, err := e.ensureTrashDirectory()
	if err != nil {
		return "", err
	}

	var id string
	var itemDirectory string
	for attempt := 0; attempt < 3; attempt++ {
		randomID := make([]byte, 16)
		if _, err := rand.Read(randomID); err != nil {
			return "", fmt.Errorf("generate trash identifier: %w", err)
		}
		id = hex.EncodeToString(randomID)
		itemDirectory = filepath.Join(trashRoot, id)
		if err := os.Mkdir(itemDirectory, 0700); err == nil {
			break
		} else if !os.IsExist(err) || attempt == 2 {
			return "", fmt.Errorf("create trash entry: %w", err)
		}
	}

	record := trashRecord{ID: id, Path: relativePath, DeletedAt: time.Now().UTC()}
	recordContent, err := json.Marshal(record)
	if err != nil {
		_ = os.Remove(itemDirectory)
		return "", fmt.Errorf("encode trash record: %w", err)
	}
	dataPath := filepath.Join(itemDirectory, "data")
	if err := os.Rename(path, dataPath); err != nil {
		_ = os.Remove(itemDirectory)
		return "", fmt.Errorf("move file to trash: %w", err)
	}

	recordPath := filepath.Join(itemDirectory, "record.json")
	recordFile, err := os.OpenFile(recordPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := recordFile.Write(recordContent)
		syncErr := recordFile.Sync()
		closeErr := recordFile.Close()
		err = errors.Join(writeErr, syncErr, closeErr)
	}
	if err != nil {
		restoreErr := os.Rename(dataPath, path)
		if restoreErr == nil {
			_ = os.Remove(recordPath)
			_ = os.Remove(itemDirectory)
		}
		if restoreErr != nil {
			return "", errors.Join(
				fmt.Errorf("save trash record: %w", err),
				fmt.Errorf("restore original file: %w", restoreErr),
			)
		}
		return "", fmt.Errorf("save trash record: %w", err)
	}
	return id, nil
}

func (e *Engine) listTrash() (Result, error) {
	trashRoot := filepath.Join(e.root, trashDirectory)
	info, err := os.Lstat(trashRoot)
	if os.IsNotExist(err) {
		return Result{Output: "Trash is empty."}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect trash: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Result{}, errors.New("trash location is not a safe directory")
	}
	resolved, err := filepath.EvalSymlinks(trashRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve trash directory: %w", err)
	}
	if !withinRoot(e.root, resolved) {
		return Result{}, errors.New("trash directory must stay inside the workspace")
	}
	if err := validateTrashMarker(resolved); err != nil {
		return Result{}, err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("list trash: %w", err)
	}
	var records []trashRecord
	for _, entry := range entries {
		if entry.Name() == trashMarker {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("unexpected entry in trash: %s", entry.Name())
		}
		record, err := readTrashRecord(filepath.Join(resolved, entry.Name()))
		if err != nil {
			return Result{}, err
		}
		if record.ID != entry.Name() {
			return Result{}, fmt.Errorf("trash record identifier mismatch: %s", entry.Name())
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return Result{Output: "Trash is empty."}, nil
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].DeletedAt.After(records[j].DeletedAt)
	})
	var output strings.Builder
	output.WriteString("Recoverable trash:\n")
	for _, record := range records {
		fmt.Fprintf(&output, "  %s  %s  %s\n", record.ID, record.Path, record.DeletedAt.Format(time.RFC3339))
	}
	output.WriteString("Restore an item with: restore <id>")
	return Result{Output: output.String()}, nil
}

func (e *Engine) restoreFromTrash(id string) (Result, error) {
	if len(id) != 32 {
		return Result{}, errors.New("invalid trash identifier")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return Result{}, errors.New("invalid trash identifier")
	}
	trashRoot, err := e.ensureTrashDirectoryExists()
	if err != nil {
		return Result{}, err
	}
	itemDirectory := filepath.Join(trashRoot, id)
	record, err := readTrashRecord(itemDirectory)
	if err != nil {
		return Result{}, err
	}
	if record.ID != id {
		return Result{}, errors.New("trash record identifier mismatch")
	}
	relative, err := e.relativePath(record.Path)
	if err != nil || relative == trashDirectory || strings.HasPrefix(relative, trashDirectory+string(filepath.Separator)) {
		return Result{}, errors.New("trash record contains an invalid restore path")
	}
	allowed, err := e.permissionAllowed("create_file", e.root)
	if err != nil {
		return Result{}, err
	}
	if !allowed {
		return Result{}, errors.New("restoring files is not permitted in this workspace")
	}
	destination, err := e.newFilePath(relative)
	if err != nil {
		return Result{}, fmt.Errorf("resolve restore destination: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, errors.New("restore destination already exists")
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect restore destination: %w", err)
	}

	dataPath := filepath.Join(itemDirectory, "data")
	dataInfo, err := os.Lstat(dataPath)
	if err != nil {
		return Result{}, fmt.Errorf("inspect trashed file: %w", err)
	}
	if dataInfo.Mode()&os.ModeSymlink != 0 || !dataInfo.Mode().IsRegular() {
		return Result{}, errors.New("trashed item is not a regular file")
	}
	if err := os.Rename(dataPath, destination); err != nil {
		return Result{}, fmt.Errorf("restore file: %w", err)
	}
	recordPath := filepath.Join(itemDirectory, "record.json")
	if err := os.Remove(recordPath); err != nil {
		return Result{}, fmt.Errorf("file restored, but trash record could not be removed: %w", err)
	}
	if err := os.Remove(itemDirectory); err != nil {
		return Result{}, fmt.Errorf("file restored, but trash entry could not be removed: %w", err)
	}
	return Result{Output: "Restored " + relative + "."}, nil
}

func (e *Engine) ensureTrashDirectory() (string, error) {
	trashRoot := filepath.Join(e.root, trashDirectory)
	created := false
	if err := os.Mkdir(trashRoot, 0700); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("create trash directory: %w", err)
		}
	} else {
		created = true
	}
	info, err := os.Lstat(trashRoot)
	if err != nil {
		return "", fmt.Errorf("inspect trash directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("trash location is not a safe directory")
	}
	resolved, err := filepath.EvalSymlinks(trashRoot)
	if err != nil {
		return "", fmt.Errorf("resolve trash directory: %w", err)
	}
	if !withinRoot(e.root, resolved) {
		return "", errors.New("trash directory must stay inside the workspace")
	}
	if created {
		markerPath := filepath.Join(resolved, trashMarker)
		if err := os.WriteFile(markerPath, []byte(trashVersion), 0600); err != nil {
			_ = os.Remove(markerPath)
			_ = os.Remove(trashRoot)
			return "", fmt.Errorf("mark trash directory: %w", err)
		}
	} else if err := validateTrashMarker(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (e *Engine) ensureTrashDirectoryExists() (string, error) {
	trashRoot := filepath.Join(e.root, trashDirectory)
	info, err := os.Lstat(trashRoot)
	if err != nil {
		return "", fmt.Errorf("inspect trash directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("trash location is not a safe directory")
	}
	resolved, err := filepath.EvalSymlinks(trashRoot)
	if err != nil {
		return "", fmt.Errorf("resolve trash directory: %w", err)
	}
	if !withinRoot(e.root, resolved) {
		return "", errors.New("trash directory must stay inside the workspace")
	}
	if err := validateTrashMarker(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func validateTrashMarker(trashRoot string) error {
	markerPath := filepath.Join(trashRoot, trashMarker)
	info, err := os.Lstat(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("workspace already contains an unrecognized trash directory")
		}
		return fmt.Errorf("inspect trash marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("trash marker is not a regular file")
	}
	content, err := os.ReadFile(markerPath)
	if err != nil {
		return fmt.Errorf("read trash marker: %w", err)
	}
	if string(content) != trashVersion {
		return errors.New("trash directory version is not supported")
	}
	return nil
}

func readTrashRecord(itemDirectory string) (trashRecord, error) {
	itemInfo, err := os.Lstat(itemDirectory)
	if err != nil {
		return trashRecord{}, fmt.Errorf("inspect trash entry: %w", err)
	}
	if itemInfo.Mode()&os.ModeSymlink != 0 || !itemInfo.IsDir() {
		return trashRecord{}, errors.New("trash entry is not a safe directory")
	}
	recordPath := filepath.Join(itemDirectory, "record.json")
	recordInfo, err := os.Lstat(recordPath)
	if err != nil {
		return trashRecord{}, fmt.Errorf("inspect trash record: %w", err)
	}
	if recordInfo.Mode()&os.ModeSymlink != 0 || !recordInfo.Mode().IsRegular() {
		return trashRecord{}, errors.New("trash record is not a regular file")
	}
	content, err := os.ReadFile(recordPath)
	if err != nil {
		return trashRecord{}, fmt.Errorf("read trash record: %w", err)
	}
	var record trashRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return trashRecord{}, fmt.Errorf("decode trash record: %w", err)
	}
	if record.ID == "" || record.Path == "" {
		return trashRecord{}, errors.New("trash record is incomplete")
	}
	return record, nil
}
