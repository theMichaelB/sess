package session

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "sync"
    "syscall"
    "time"
)

const (
	sessionDir     = ".sess"
	currentFile    = ".current_session"
	lockFile       = ".lock"
	lockTimeout    = 5 * time.Second
	sessionPattern = "session-%s"
)

type Manager struct {
	baseDir string
	mu      sync.Mutex
}

type Session struct {
	Number    string    `json:"session_num"`
	CreatedAt time.Time `json:"created_at"`
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
}

type LockFile struct {
	file *os.File
}

// CurrentSessionInfo represents the currently attached client state
type CurrentSessionInfo struct {
	Number string `json:"number"`
	PID    int    `json:"pid"`
}

func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	baseDir := filepath.Join(homeDir, sessionDir)
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	return &Manager{
		baseDir: baseDir,
	}, nil
}

func (m *Manager) acquireLock() (*LockFile, error) {
	lockPath := filepath.Join(m.baseDir, lockFile)

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			deadline := time.Now().Add(lockTimeout)
			for time.Now().Before(deadline) {
				file, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				return nil, fmt.Errorf("failed to acquire lock: %w", err)
			}
		} else {
			return nil, err
		}
	}

	return &LockFile{file: file}, nil
}

func (l *LockFile) Release() {
	if l.file != nil {
		path := l.file.Name()
		l.file.Close()
		os.Remove(path)
	}
}

func (m *Manager) NextSessionNumber() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lock, err := m.acquireLock()
	if err != nil {
		return "", err
	}
	defer lock.Release()

	sessions, err := m.listSessionsUnsafe()
	if err != nil {
		return "", err
	}

	maxNum := 0
	for _, session := range sessions {
		num, err := strconv.Atoi(session.Number)
		if err == nil && num > maxNum {
			maxNum = num
		}
	}

	return fmt.Sprintf("%03d", maxNum+1), nil
}

func (m *Manager) CreateSession(number, socketPath, metaPath, shell string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	lock, err := m.acquireLock()
	if err != nil {
		return err
	}
	defer lock.Release()

	session := Session{
		Number:    number,
		CreatedAt: time.Now(),
		PID:       os.Getpid(),
		Command:   shell,
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, metaPath)
}

func (m *Manager) GetSession(number string) (*Session, error) {
	metaPath := m.GetMetaPath(number)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %s does not exist", number)
		}
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}

	if !m.isProcessAlive(session.PID) {
		m.cleanupSession(number)
		return nil, fmt.Errorf("session %s is dead", number)
	}

	return &session, nil
}

func (m *Manager) ListSessions() ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lock, err := m.acquireLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	return m.listSessionsUnsafe()
}

func (m *Manager) listSessionsUnsafe() ([]Session, error) {
	pattern := filepath.Join(m.baseDir, "session-*.meta")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, metaPath := range matches {
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		if !m.isProcessAlive(session.PID) {
			base := filepath.Base(metaPath)
			number := strings.TrimPrefix(base, "session-")
			number = strings.TrimSuffix(number, ".meta")
			m.cleanupSession(number)
			continue
		}

		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Number < sessions[j].Number
	})

	return sessions, nil
}

func (m *Manager) KillSession(number string) error {
	session, err := m.GetSession(number)
	if err != nil {
		return err
	}

	if err := syscall.Kill(session.PID, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			m.cleanupSession(number)
			return fmt.Errorf("session %s is already dead", number)
		}
		return err
	}

	time.Sleep(1 * time.Second)

	if m.isProcessAlive(session.PID) {
		syscall.Kill(session.PID, syscall.SIGKILL)
	}

	m.cleanupSession(number)
	return nil
}

func (m *Manager) SetCurrentSession(number string) error {
	currentPath := filepath.Join(m.baseDir, currentFile)
	tmpPath := currentPath + ".tmp"

	info := CurrentSessionInfo{Number: number, PID: os.Getpid()}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, currentPath)
}

func (m *Manager) GetCurrentSession() (string, error) {
	info, err := m.readCurrentSessionInfo()
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}

	// If the client process that set the current session is no longer alive,
	// clear the marker and report no active attachment.
	if info.PID != 0 && !m.isProcessAlive(info.PID) {
		currentPath := filepath.Join(m.baseDir, currentFile)
		os.Remove(currentPath)
		return "", nil
	}

	// Validate session still exists; cleanup if stale
	if _, err := m.GetSession(info.Number); err != nil {
		currentPath := filepath.Join(m.baseDir, currentFile)
		os.Remove(currentPath)
		return "", nil
	}

	return info.Number, nil
}

// GetCurrentSessionInfo returns number and client PID if present
func (m *Manager) GetCurrentSessionInfo() (*CurrentSessionInfo, error) {
	return m.readCurrentSessionInfo()
}

func (m *Manager) readCurrentSessionInfo() (*CurrentSessionInfo, error) {
	currentPath := filepath.Join(m.baseDir, currentFile)
	data, err := os.ReadFile(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Backward compatibility: if legacy content is plain number
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) > 0 && trimmed[0] != '{' {
		// No PID available in legacy format
		return &CurrentSessionInfo{Number: trimmed, PID: 0}, nil
	}

	var info CurrentSessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (m *Manager) ClearCurrentSession() error {
	currentPath := filepath.Join(m.baseDir, currentFile)
	return os.Remove(currentPath)
}

func (m *Manager) GetSocketPath(number string) string {
	return filepath.Join(m.baseDir, fmt.Sprintf("session-%s.sock", number))
}

func (m *Manager) GetMetaPath(number string) string {
	return filepath.Join(m.baseDir, fmt.Sprintf("session-%s.meta", number))
}

func (m *Manager) IsInSession() bool {
	return os.Getenv("SESS_NUM") != ""
}

func (m *Manager) CurrentSessionNumber() string {
	return os.Getenv("SESS_NUM")
}

func (m *Manager) isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

func (m *Manager) cleanupSession(number string) {
	socketPath := m.GetSocketPath(number)
	metaPath := m.GetMetaPath(number)

	os.Remove(socketPath)
	os.Remove(metaPath)

	current, _ := m.GetCurrentSession()
	if current == number {
		m.ClearCurrentSession()
	}
}

func (m *Manager) NormalizeSessionNumber(number string) string {
	// Convert "1" to "001", "12" to "012", etc.
	num, err := strconv.Atoi(number)
	if err != nil {
		return number // Return as-is if not a number
	}
	return fmt.Sprintf("%03d", num)
}
