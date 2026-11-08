// Package mc starts, stops and talks to Minecraft server processes.
package mc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// A server ID must only use lowercase letter, numbers, and dashes,
// so nobody can sneak in "../" and escape the servers folder.
var validID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var (
	ErrBadID          = errors.New("server id may only use a-z, 0-9 and -")
	ErrNoJar          = errors.New("server.jar not found in the server folder")
	ErrAlreadyRunning = errors.New("server is already running")
	ErrNotRunning     = errors.New("server is not running")
)

// Manager keeps track of every Minecraft server on this machine.
type Manager struct {
	Root string // folder that holds on sub-folder per server

	mu      sync.Mutex
	servers map[string]*server
}

func NewManager(root string) *Manager {
	return &Manager{Root: root, servers: map[string]*server{}}
}

// server is one Minecraft server: its process, its console history and
// everyone currently watching its console.
type server struct {
	mu       sync.Mutex
	running  bool
	cmd      *exec.Cmd
	stdin    io.WriteCloser
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

// Running reports whether the server's proccess is alive.
func (m *Manager) Running(id string) (bool, error) {
	s, err := m.get(id)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, nil
}

// Start launches "java -jar server.jar no gui" inside the server's folder.
func (m *Manager) Start(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.Root, id)
	if _, err := os.Stat(filepath.Join(dir, "server.jar")); err != nil {
		return ErrNoJar
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return ErrAlreadyRunning
	}

	cmd := exec.Command("java", "-Xms1G", "-Xmx4G", "-jar", "server.jar", "nogui")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start java: %w", err)
	}
	s.cmd, s.stdin, s.running = cmd, stdin, true

	// Read the console line by line in the background
	go func() {
		var readers sync.WaitGroup
		readOutput := func(output io.Reader) {
			defer readers.Done()
			scanner := bufio.NewScanner(output)
			for scanner.Scan() {
				s.publish(scanner.Text())
			}
		}
		readers.Add(2)
		go readOutput(stdout)
		go readOutput(stderr)
		readers.Wait()
		err := cmd.Wait() // the process has exited
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		if err != nil {
			s.publish("[Tinker] Server stopped: " + err.Error())
		} else {
			s.publish("[Tinker] Server stopped!")
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

// Send types a command into the server console, like "say hi" or "op Steve"
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

// Watch returns the recent console history plus a channel that recieves
// every new line. Call the returned stop funtion where you're done.
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
