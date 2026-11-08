package mc

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Where Crafty Controller keeps its servers. Each server is a folder
// named with a long random ID, like 3f2a…-…-…
var CraftyFolders = []string{
	"/var/opt/minecraft/crafty/crafty-4/servers", // Crafty's normal Linux install
	"/opt/crafty/docker/servers",                 // a common Docker setup
}

// Importable is a folder that looks like a Minecraft server.
type Importable struct {
	Path          string
	SuggestedName string
	Jar           string
	Port          int
	Source        string // "crafty" or "folder"
}

// FindImportable looks for servers to import. With no folder, it looks
// in Crafty's usual places (and ~/crafty/docker/servers). With a
// folder, it checks that folder and the folders directly inside it.
func (m *Manager) FindImportable(folder string) ([]Importable, error) {
	roots := map[string]string{} // folder -> source
	if folder == "" {
		for _, f := range CraftyFolders {
			roots[f] = "crafty"
		}
		if home, err := os.UserHomeDir(); err == nil {
			roots[filepath.Join(home, "crafty", "docker", "servers")] = "crafty"
		}
	} else {
		if !filepath.IsAbs(folder) {
			return nil, fmt.Errorf("%w: use a full path like /home/you/servers", ErrBadPath)
		}
		if _, err := os.Stat(folder); err != nil {
			return nil, explainFS(err, folder)
		}
		roots[filepath.Clean(folder)] = "folder"
	}

	var found []Importable
	for root, source := range roots {
		candidates := []string{root}
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					candidates = append(candidates, filepath.Join(root, e.Name()))
				}
			}
		} else if folder != "" {
			return nil, explainFS(err, root)
		}
		for _, dir := range candidates {
			if m.insideRoot(dir) || !looksLikeServer(dir) {
				continue
			}
			found = append(found, Importable{
				Path:          dir,
				SuggestedName: suggestName(dir),
				Jar:           pickJar(dir),
				Port:          readPort(dir),
				Source:        source,
			})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found, nil
}

// Import COPIES a server folder into Tinker's servers folder. The
// original is left untouched, so it still works in Crafty if you
// change your mind.
func (m *Manager) Import(path, name, jar string, memoryMB int) (ServerInfo, error) {
	if !filepath.IsAbs(path) {
		return ServerInfo{}, fmt.Errorf("%w: use a full path", ErrBadPath)
	}
	path = filepath.Clean(path)
	if m.insideRoot(path) {
		return ServerInfo{}, fmt.Errorf("%w: that server is already in Tinker", ErrBadPath)
	}
	if !looksLikeServer(path) {
		return ServerInfo{}, fmt.Errorf("%w: no Minecraft server found in %s", ErrBadPath, path)
	}
	if jar == "" {
		jar = pickJar(path)
	}
	if jar != filepath.Base(jar) || !strings.HasSuffix(jar, ".jar") {
		return ServerInfo{}, fmt.Errorf("%w: jar must be a .jar file name inside the folder", ErrBadPath)
	}
	if _, err := os.Stat(filepath.Join(path, jar)); err != nil {
		return ServerInfo{}, explainFS(err, filepath.Join(path, jar))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = suggestName(path)
	}
	if memoryMB == 0 {
		memoryMB = 2048
	}
	if memoryMB < 512 {
		return ServerInfo{}, ErrBadMemory
	}
	// Copying a world while it's running can corrupt it. If the server's
	// port is busy, it's probably still running in Crafty.
	if port := readPort(path); portInUse(port) {
		return ServerInfo{}, fmt.Errorf("%w (port %d is busy). Stop it in Crafty first, then import", ErrSourceRunning, port)
	}

	id, dir, err := m.makeFolder(name)
	if err != nil {
		return ServerInfo{}, err
	}
	if err := copyDir(path, dir); err != nil {
		os.RemoveAll(dir)
		return ServerInfo{}, explainFS(err, path)
	}
	kind, version := guessType(jar)
	if err := saveSettings(dir, Settings{Name: name, Type: kind, Version: version, Jar: jar, MemoryMB: memoryMB}); err != nil {
		os.RemoveAll(dir)
		return ServerInfo{}, err
	}
	return m.Info(id)
}

func (m *Manager) insideRoot(dir string) bool {
	rel, err := filepath.Rel(m.Root, dir)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// A folder is a server if it has server.properties or eula.txt, plus a jar.
func looksLikeServer(dir string) bool {
	_, e1 := os.Stat(filepath.Join(dir, "server.properties"))
	_, e2 := os.Stat(filepath.Join(dir, "eula.txt"))
	return (e1 == nil || e2 == nil) && pickJar(dir) != ""
}

// pickJar guesses which jar starts the server. Forge/NeoForge servers
// start with a script instead of a jar, so they aren't supported yet.
func pickJar(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var jars []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			jars = append(jars, e.Name())
		}
	}
	if len(jars) == 0 {
		return ""
	}
	sort.Strings(jars)
	for _, hint := range []string{"paper", "purpur", "fabric", "server"} {
		for _, j := range jars {
			if strings.Contains(strings.ToLower(j), hint) {
				return j
			}
		}
	}
	return jars[0]
}

var versionInName = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// guessType reads the type and version from a jar name like
// "paper-1.21.4-232.jar".
func guessType(jar string) (kind, version string) {
	lower := strings.ToLower(jar)
	switch {
	case strings.Contains(lower, "paper"), strings.Contains(lower, "purpur"):
		kind = "paper"
	case strings.Contains(lower, "fabric"):
		kind = "fabric"
	case strings.HasPrefix(lower, "minecraft_server"):
		kind = "vanilla"
	default:
		kind = "other"
	}
	return kind, versionInName.FindString(jar)
}

// suggestName uses the server's MOTD or world name, since Crafty's
// folder names are random IDs.
func suggestName(dir string) string {
	props := readProperties(dir)
	if motd := props["motd"]; motd != "" && motd != "A Minecraft Server" && !strings.Contains(motd, `\u`) {
		return motd
	}
	if level := props["level-name"]; level != "" && level != "world" {
		return level
	}
	return filepath.Base(dir)
}

func portInUse(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// copyDir copies every file and folder from src into dst (which exists).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm()|0o600)
		default:
			return nil // skip shortcuts (symlinks) and other special files
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// explainFS turns "permission denied" into something you can act on.
func explainFS(err error, path string) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: the Agent isn't allowed to read %s. On the server PC run: sudo chmod -R o+rX %q", ErrNoAccess, path, path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s doesn't exist", ErrBadPath, path)
	}
	return err
}
