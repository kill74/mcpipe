package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"mcpipe/internal/config"
)

type Lockfile struct {
	Version     string            `json:"version"`
	PipelineSHA string            `json:"pipeline_sha256"`
	Schema      string            `json:"schema,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	MCPServers  map[string]string `json:"mcp_servers,omitempty"`
	Models      []string          `json:"models,omitempty"`
	Tools       []string          `json:"tools,omitempty"`
}

func DefaultLockPath(pipelinePath string) string {
	dir := filepath.Dir(pipelinePath)
	base := filepath.Base(pipelinePath)
	return filepath.Join(dir, base+".lock")
}

func BuildLock(pipelinePath string, p *config.Pipeline, now time.Time) (Lockfile, error) {
	sha, err := FileSHA256(pipelinePath)
	if err != nil {
		return Lockfile{}, err
	}
	lock := Lockfile{
		Version:     "1",
		PipelineSHA: sha,
		Schema:      p.Schema,
		CreatedAt:   now.UTC(),
		MCPServers:  map[string]string{},
	}
	for name, server := range p.MCPServers {
		lock.MCPServers[name] = server.Transport + ":" + server.Command
	}
	modelSet := map[string]bool{}
	toolSet := map[string]bool{}
	for _, step := range p.Steps {
		effective := p.EffectiveStep(step)
		if effective.LLM.Backend != "" || effective.LLM.Model != "" {
			modelSet[effective.LLM.Backend+"/"+effective.LLM.Model] = true
		}
		for _, rule := range step.Tools.Allow {
			toolSet[rule] = true
		}
	}
	for model := range modelSet {
		lock.Models = append(lock.Models, model)
	}
	for tool := range toolSet {
		lock.Tools = append(lock.Tools, tool)
	}
	sort.Strings(lock.Models)
	sort.Strings(lock.Tools)
	return lock, nil
}

func WriteLock(path string, lock Lockfile) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func ReadLock(path string) (Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Lockfile{}, err
	}
	var lock Lockfile
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lockfile{}, err
	}
	return lock, nil
}

func VerifyLock(path string, pipelinePath string) error {
	lock, err := ReadLock(path)
	if err != nil {
		return err
	}
	current, err := FileSHA256(pipelinePath)
	if err != nil {
		return err
	}
	if lock.PipelineSHA != current {
		return fmt.Errorf("pipeline hash mismatch: lock has %s, current is %s", lock.PipelineSHA, current)
	}
	return nil
}

func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var compact bytes.Buffer
	if json.Valid(data) {
		if err := json.Compact(&compact, data); err == nil {
			data = compact.Bytes()
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
