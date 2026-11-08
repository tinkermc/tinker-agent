package mc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CreateOptions is what the user filled in on Dash's "New server" form.
type CreateOptions struct {
	Name       string
	Type       string // "vanilla" (default), "paper" or "fabric"
	Version    string // empty = latest release
	MemoryMB   int    // 0 = 2048
	Port       int    // 0 = first free port from 25565
	AcceptEULA bool
}

// Create makes a brand new server: a folder, the downloaded jar,
// eula.txt, server.properties and tinker.json. If anything fails
// halfway, the half-made folder is deleted.
func (m *Manager) Create(ctx context.Context, o CreateOptions) (ServerInfo, error) {
	if !o.AcceptEULA {
		return ServerInfo{}, ErrEULA
	}
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" || len(o.Name) > 64 {
		return ServerInfo{}, ErrBadName
	}
	if o.Type == "" {
		o.Type = "vanilla"
	}
	if o.MemoryMB == 0 {
		o.MemoryMB = 2048
	}
	if o.MemoryMB < 512 {
		return ServerInfo{}, ErrBadMemory
	}
	if o.Port == 0 {
		o.Port = m.freePort()
	}
	if o.Port < 1024 || o.Port > 65535 {
		return ServerInfo{}, ErrBadPort
	}

	id, dir, err := m.makeFolder(o.Name)
	if err != nil {
		return ServerInfo{}, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir) // clean up the half-made server
		}
	}()

	version, err := downloadJar(ctx, o.Type, o.Version, dir)
	if err != nil {
		return ServerInfo{}, err
	}
	// The user agreed to the EULA in Dash, so we can write this for them.
	eula := "# Accepted by the user in Tinker Dash: https://aka.ms/MinecraftEULA\neula=true\n"
	if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte(eula), 0o644); err != nil {
		return ServerInfo{}, err
	}
	// Minecraft fills in every other setting on first start.
	props := fmt.Sprintf("server-port=%d\nmotd=%s\n", o.Port, propertiesEscape(o.Name))
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte(props), 0o644); err != nil {
		return ServerInfo{}, err
	}
	err = saveSettings(dir, Settings{Name: o.Name, Type: o.Type, Version: version, Jar: "server.jar", MemoryMB: o.MemoryMB})
	if err != nil {
		return ServerInfo{}, err
	}
	ok = true
	return m.Info(id)
}

// makeFolder turns a name like "My Survival!" into an ID like
// "my-survival" and creates its folder. If the ID is taken it tries
// "my-survival-2", "my-survival-3", and so on.
func (m *Manager) makeFolder(name string) (id, dir string, err error) {
	base := slugify(name)
	for i := 1; i < 100; i++ {
		id = base
		if i > 1 {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		dir = filepath.Join(m.Root, id)
		err = os.Mkdir(dir, 0o755) // fails if the folder already exists
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("too many servers called %q", name)
}

func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 32 {
		id = strings.Trim(id[:32], "-")
	}
	if id == "" {
		id = "server"
	}
	return id
}

// freePort returns the first port from 25565 that no Tinker server uses.
func (m *Manager) freePort() int {
	used := map[int]bool{}
	list, _ := m.List()
	for _, s := range list {
		used[s.Port] = true
	}
	p := 25565
	for used[p] {
		p++
	}
	return p
}

// server.properties can only hold plain ASCII, so other characters
// (like é or emoji) are written as \uXXXX.
func propertiesEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r < 32:
			// skip control characters like newlines
		case r < 127:
			b.WriteRune(r)
		case r <= 0xFFFF:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}
