package mc

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Settings is what Tinker remembers about one server.
// It's saved as tinker.json inside the server's folder.
type Settings struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "vanilla", "paper", "fabric" or "other"
	Version  string `json:"version"`
	Jar      string `json:"jar"`      // the file Java runs, like "server.jar"
	MemoryMB int    `json:"memoryMb"` // max RAM for Java
}

const settingsFile = "tinker.json"

// loadSettings reads tinker.json. Servers made before Part 2 don't have
// one, so they get sensible defaults (the same ones Part 1 used).
func loadSettings(dir, id string) Settings {
	s := Settings{}
	if b, err := os.ReadFile(filepath.Join(dir, settingsFile)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Name == "" {
		s.Name = id
	}
	if s.Type == "" {
		s.Type = "other"
	}
	if s.Jar == "" {
		s.Jar = "server.jar"
	}
	if s.MemoryMB <= 0 {
		s.MemoryMB = 4096
	}
	return s
}

func saveSettings(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, settingsFile), b, 0o644)
}

// readProperties reads Minecraft's server.properties file into a map.
func readProperties(dir string) map[string]string {
	props := map[string]string{}
	f, err := os.Open(filepath.Join(dir, "server.properties"))
	if err != nil {
		return props
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return props
}

// readPort returns the port players join on (25565 if it isn't set).
func readPort(dir string) int {
	if p, err := strconv.Atoi(readProperties(dir)["server-port"]); err == nil && p > 0 {
		return p
	}
	return 25565
}

// ServerInfo is everything Dash needs to show one server.
type ServerInfo struct {
	ID string
	Settings
	Port    int
	Running bool
}

// Info describes one server.
func (m *Manager) Info(id string) (ServerInfo, error) {
	s, err := m.get(id)
	if err != nil {
		return ServerInfo{}, err
	}
	dir := filepath.Join(m.Root, id)
	if _, err := os.Stat(dir); err != nil {
		return ServerInfo{}, ErrNotFound
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	return ServerInfo{ID: id, Settings: loadSettings(dir, id), Port: readPort(dir), Running: running}, nil
}

// List describes every server folder, sorted by name.
func (m *Manager) List() ([]ServerInfo, error) {
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return nil, err
	}
	var list []ServerInfo
	for _, e := range entries {
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		if info, err := m.Info(e.Name()); err == nil {
			list = append(list, info)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}
