package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const stateVersion = 2

type FileStateStore struct {
	path string
}

func NewFileStateStore(path string) *FileStateStore {
	return &FileStateStore{path: path}
}

func (st *FileStateStore) Exists() bool {
	_, err := os.Stat(st.path)
	return err == nil
}

func (st *FileStateStore) Load() (*RunState, error) {
	data, err := os.ReadFile(st.path)
	if err != nil {
		return nil, err
	}
	var s RunState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: unreadable state; run --abort", st.path)
	}
	if s.Version != stateVersion {
		return nil, fmt.Errorf("%s: unsupported state version %d; run --abort", st.path, s.Version)
	}
	return &s, nil
}

func (st *FileStateStore) PeekStatus() string {
	data, err := os.ReadFile(st.path)
	if err != nil {
		return "unreadable"
	}
	var s struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &s) != nil || s.Status == "" {
		return "unreadable"
	}
	return s.Status
}

func (st *FileStateStore) Save(s *RunState) error {
	s.Version = stateVersion
	return writeJSONFile(st.path, s)
}

func (st *FileStateStore) Clear() error {
	if err := os.Remove(st.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type ProcessLock struct {
	path string
	file *os.File
	log  *slog.Logger
}

func NewProcessLock(path string, log *slog.Logger) *ProcessLock {
	return &ProcessLock{path: path, log: log}
}

func (l *ProcessLock) Acquire() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return fmt.Errorf("assemble already running: %s", l.path)
	}
	data, _ := os.ReadFile(l.path)
	text := strings.TrimSpace(string(data))
	if pid, atoiErr := strconv.Atoi(text); atoiErr == nil && pid != os.Getpid() && pidAlive(pid) {
		unlockAndClose(f)
		return fmt.Errorf("another assemble (pid %d) is running", pid)
	}
	if text != "" {
		l.log.Warn("replacing stale lock", slog.String("pid", text))
	}
	if err := f.Truncate(0); err != nil {
		unlockAndClose(f)
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		unlockAndClose(f)
		return err
	}
	if _, err := fmt.Fprintf(f, "%d", os.Getpid()); err != nil {
		unlockAndClose(f)
		return err
	}
	l.file = f
	return nil
}

func (l *ProcessLock) Release() {
	if l.file != nil {
		unlockAndClose(l.file)
	}
	os.Remove(l.path)
}

func unlockAndClose(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

type logManager struct {
	stderr  io.Writer
	verbose bool
	file    *os.File
	logger  *slog.Logger
}

func newLogManager(stderr io.Writer, verbose bool) *logManager {
	m := &logManager{stderr: stderr, verbose: verbose}
	m.logger = slog.New(newFanoutHandler(m, slog.LevelInfo))
	return m
}

func (m *logManager) Logger() *slog.Logger {
	return m.logger
}

func (m *logManager) AttachWorkspace(ws string) {
	if m.file != nil {
		return
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(ws, ".git", "fork-assemble.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		m.file = f
	}
}

func (m *logManager) Close() {
	if m.file != nil {
		m.file.Close()
	}
}

type fanoutHandler struct {
	manager *logManager
	level   slog.Level
}

func newFanoutHandler(manager *logManager, level slog.Level) *fanoutHandler {
	return &fanoutHandler{manager: manager, level: level}
}

func (h *fanoutHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelDebug
}

func (h *fanoutHandler) Handle(_ context.Context, r slog.Record) error {
	var line strings.Builder
	line.WriteString(r.Message)
	r.Attrs(func(attr slog.Attr) bool {
		writeAttr(&line, attr)
		return true
	})
	line.WriteByte('\n')
	if h.manager.file != nil {
		h.manager.file.WriteString(line.String())
	}
	if r.Level >= h.level || h.manager.verbose {
		h.manager.stderr.Write([]byte(line.String()))
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *fanoutHandler) WithGroup(_ string) slog.Handler { return h }

func writeAttr(line *strings.Builder, attr slog.Attr) {
	line.WriteByte(' ')
	line.WriteString(attr.Key)
	line.WriteByte('=')
	line.WriteString(attr.Value.String())
}
