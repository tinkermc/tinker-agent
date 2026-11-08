// Package mc creates, starts, stops and talks to Minecraft servers.
package mc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// A server ID may only use lowercase letters, numbers and dashes,
// so nobody can sneak in "../" and escape the servers folder.
var validID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var (
	ErrBadID          = errors.New("server id may only use a-z, 0-9 and -")
	ErrNoJar          = errors.New("the server's jar file is missing from its folder")
	ErrAlreadyRunning = errors.New("server is already running")
	ErrNotRunning     = errors.New("server is not running")
	// NEW in Part 2
	ErrNotFound      = errors.New("no server with that id")
	ErrEULA          = errors.New("you must accept the Minecraft EULA (https://aka.ms/MinecraftEULA) to create a server")
	ErrBadName       = errors.New("give the server a name (up to 64 characters)")
	ErrBadType       = errors.New("Tinker can't create that type of server yet")
	ErrBadVersion    = errors.New("unsupported version")
	ErrBadMemory     = errors.New("memory must be at least 512 MB")
	ErrBadPort       = errors.New("port must be between 1024 and 65535")
	ErrBadPath       = errors.New("can't use that folder")
	ErrNoAccess      = errors.New("permission denied")
	ErrSourceRunning = errors.New("that server looks like it's still running")
)

// Manager keeps track of every Minecraft server on this machine.
type Manager struct {
	Root string // folder that holds one sub-folder per server
	Java string // the java program to run (NEW in Part 2)

	mu      sync.Mutex
	servers map[string]*server
}

func NewManager(root, java string) *Manager {
	if java == "" {
		java = "java"
	}
	return &Manager{Root: root, Java: java, servers: map[string]*server{}}
}

// server is one Minecraft server: its process, its console history and
// everyone currently watching its console.
type server struct {
	mu       sync.Mutex
	running  bool
	stdin    io.WriteCloser
	cmd      *exec.Cmd
	history  []string
	watchers map[chan string]bool
}

func (m *Manager) get(id string) (*server, error) {
	if !validID.MatchString(id) {
		return nil, ErrBadID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[id]
	if !ok {
		s = &server{watchers: map[chan string]bool{}}
		m.servers[id] = s
	}
	return s, nil
}

// Running reports whether the server's process is alive.
func (m *Manager) Running(id string) (bool, error) {
	s, err := m.get(id)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, nil
}

// Start launches Java inside the server's folder, using the jar and
// memory saved in tinker.json (CHANGED in Part 2).
func (m *Manager) Start(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.Root, id)
	if _, err := os.Stat(dir); err != nil {
		return ErrNotFound
	}
	cfg := loadSettings(dir, id)
	if _, err := os.Stat(filepath.Join(dir, cfg.Jar)); err != nil {
		return ErrNoJar
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return ErrAlreadyRunning
	}

	minMem := min(1024, cfg.MemoryMB)
	cmd := exec.Command(m.Java,
		fmt.Sprintf("-Xms%dM", minMem),
		fmt.Sprintf("-Xmx%dM", cfg.MemoryMB),
		"-jar", cfg.Jar, "nogui")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // errors go to the same console
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start java: %w", err)
	}
	s.cmd, s.stdin, s.running = cmd, stdin, true

	// Read the console line by line in the background.
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			s.publish(scanner.Text())
		}
		err := cmd.Wait() // the process has exited
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		if err != nil {
			s.publish("[Tinker] Server stopped: " + err.Error())
		} else {
			s.publish("[Tinker] Server stopped")
		}
	}()
	return nil
}

// Stop asks Minecraft to save and shut down. If it hasn't stopped
// after 60 seconds, the process is killed.
func (m *Manager) Stop(id string) error {
	if err := m.Send(id, "stop"); err != nil {
		return err
	}
	s, _ := m.get(id)
	go func() {
		time.Sleep(60 * time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.running && s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	}()
	return nil
}

// StopAll stops every running server and waits for them to save.
// The Agent calls this when it shuts down, so restarting or updating
// the Agent no longer kills servers without saving (NEW in Part 2).
func (m *Manager) StopAll(timeout time.Duration) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for tries := 0; time.Now().Before(deadline); tries++ {
		anyRunning := false
		for _, id := range ids {
			if r, _ := m.Running(id); r {
				anyRunning = true
				// Re-send "stop" every 10 seconds: a server that's still
				// starting up can miss the first one.
				if tries%40 == 0 {
					_ = m.Send(id, "stop")
				}
			}
		}
		if !anyRunning {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, id := range ids { // still running after the timeout: force it
		s, _ := m.get(id)
		s.mu.Lock()
		if s.running && s.cmd != nil && s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil {
				log.Printf("could not kill %s: %v", id, err)
			}
		}
		s.mu.Unlock()
	}
}

// Send types a command into the server console, like "say hi" or "op Steve".
func (m *Manager) Send(id, command string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return ErrNotRunning
	}
	_, err = io.WriteString(s.stdin, command+"\n")
	return err
}

// Watch returns the recent console history plus a channel that receives
// every new line. Call the returned stop function when you're done.
func (m *Manager) Watch(id string) (history []string, lines chan string, stop func(), err error) {
	s, err := m.get(id)
	if err != nil {
		return nil, nil, nil, err
	}
	lines = make(chan string, 256)
	s.mu.Lock()
	history = append([]string(nil), s.history...)
	s.watchers[lines] = true
	s.mu.Unlock()
	stop = func() {
		s.mu.Lock()
		delete(s.watchers, lines)
		s.mu.Unlock()
	}
	return history, lines, stop, nil
}

// publish saves a line to history and hands it to every watcher.
func (s *server) publish(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, line)
	if len(s.history) > 500 { // keep the last 500 lines
		s.history = s.history[len(s.history)-500:]
	}
	for ch := range s.watchers {
		select {
		case ch <- line:
		default: // this watcher is too slow; skip the line rather than freeze
		}
	}
}
