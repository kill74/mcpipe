package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Auditor struct {
	disabled bool
	file     *os.File
	redactor Redactor
	mu       sync.Mutex
}

func NewAuditor(dir, runID string, redactor Redactor, disabled bool) (*Auditor, error) {
	if disabled {
		return &Auditor{disabled: true, redactor: redactor}, nil
	}
	if dir == "" {
		dir = DefaultAuditDir
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, runID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &Auditor{file: file, redactor: redactor}, nil
}

func (a *Auditor) Close() error {
	if a == nil || a.disabled || a.file == nil {
		return nil
	}
	return a.file.Close()
}

func (a *Auditor) Event(kind string, fields map[string]any) {
	if a == nil || a.disabled || a.file == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	event := map[string]any{
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
		"kind": kind,
	}
	for key, value := range fields {
		event[key] = a.redactor.RedactAny(value)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = a.file.Write(append(data, '\n'))
}

func HashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
