package mc

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Some download sites ask every program to say who it is.
const userAgent = "tinker-agent (github.com/tinkermc/tinker-agent)"

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Versions look like "26.3" or "1.21.11". Checking this stops anyone
// from sneaking odd text into the download URLs.
var validVersion = regexp.MustCompile(`^[0-9A-Za-z._-]{1,32}$`)

// downloadJar downloads the server jar for this type and version into
// dir/server.jar, checks it isn't corrupted, and returns the exact
// version it used. An empty version means "the latest release".
func downloadJar(ctx context.Context, kind, version, dir string) (string, error) {
	if version == "" {
		v, err := latestRelease(ctx)
		if err != nil {
			return "", err
		}
		version = v
	}
	if !validVersion.MatchString(version) {
		return "", fmt.Errorf("%w: %q", ErrBadVersion, version)
	}

	var (
		url, want string
		h         hash.Hash
		err       error
	)
	switch kind {
	case "vanilla":
		url, want, err = vanillaJar(ctx, version)
		h = sha1.New() // Mojang publishes SHA-1 checksums
	case "paper":
		url, want, err = paperJar(ctx, version)
		h = sha256.New() // Paper publishes SHA-256 checksums
	case "fabric":
		url, err = fabricJar(ctx, version) // Fabric doesn't publish one
	default:
		return "", fmt.Errorf("%w: %q", ErrBadType, kind)
	}
	if err != nil {
		return "", err
	}
	return version, saveFile(ctx, url, filepath.Join(dir, "server.jar"), h, want)
}

// latestRelease asks Mojang for the newest full release, like "26.3".
func latestRelease(ctx context.Context) (string, error) {
	var manifest mojangManifest
	if err := getJSON(ctx, "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &manifest); err != nil {
		return "", err
	}
	return manifest.Latest.Release, nil
}

type mojangManifest struct {
	Latest struct {
		Release string `json:"release"`
	} `json:"latest"`
	Versions []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"versions"`
}

// Vanilla: the version list points to a details file, which has the jar link.
func vanillaJar(ctx context.Context, version string) (url, sha1sum string, err error) {
	var manifest mojangManifest
	if err := getJSON(ctx, "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &manifest); err != nil {
		return "", "", err
	}
	for _, v := range manifest.Versions {
		if v.ID != version {
			continue
		}
		var details struct {
			Downloads struct {
				Server struct {
					URL  string `json:"url"`
					SHA1 string `json:"sha1"`
				} `json:"server"`
			} `json:"downloads"`
		}
		if err := getJSON(ctx, v.URL, &details); err != nil {
			return "", "", err
		}
		if details.Downloads.Server.URL == "" {
			return "", "", fmt.Errorf("%w: Minecraft %s has no server download", ErrBadVersion, version)
		}
		return details.Downloads.Server.URL, details.Downloads.Server.SHA1, nil
	}
	return "", "", fmt.Errorf("%w: Minecraft %s doesn't exist", ErrBadVersion, version)
}

// Paper: ask PaperMC for the newest build of this version.
func paperJar(ctx context.Context, version string) (url, sha256sum string, err error) {
	var build struct {
		Downloads map[string]struct {
			URL       string `json:"url"`
			Checksums struct {
				SHA256 string `json:"sha256"`
			} `json:"checksums"`
		} `json:"downloads"`
	}
	err = getJSON(ctx, "https://fill.papermc.io/v3/projects/paper/versions/"+version+"/builds/latest", &build)
	if err == errNotFound {
		return "", "", fmt.Errorf("%w: Paper doesn't have a build for %s yet", ErrBadVersion, version)
	}
	if err != nil {
		return "", "", err
	}
	d, ok := build.Downloads["server:default"]
	if !ok {
		return "", "", fmt.Errorf("Paper's answer had no server download")
	}
	return d.URL, d.Checksums.SHA256, nil
}

// Fabric: pick the newest stable loader and installer for this version.
// The jar is a small launcher that downloads the rest on first start.
func fabricJar(ctx context.Context, version string) (string, error) {
	var loaders []struct {
		Loader struct {
			Version string `json:"version"`
			Stable  bool   `json:"stable"`
		} `json:"loader"`
	}
	err := getJSON(ctx, "https://meta.fabricmc.net/v2/versions/loader/"+version, &loaders)
	if err != nil && err != errNotFound {
		return "", err
	}
	loader := ""
	for _, l := range loaders {
		if l.Loader.Stable {
			loader = l.Loader.Version
			break
		}
	}
	if loader == "" {
		return "", fmt.Errorf("%w: Fabric doesn't support %s yet", ErrBadVersion, version)
	}

	var installers []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := getJSON(ctx, "https://meta.fabricmc.net/v2/versions/installer", &installers); err != nil {
		return "", err
	}
	installer := ""
	for _, i := range installers {
		if i.Stable {
			installer = i.Version
			break
		}
	}
	if installer == "" {
		return "", fmt.Errorf("couldn't find a stable Fabric installer")
	}
	return fmt.Sprintf("https://meta.fabricmc.net/v2/versions/loader/%s/%s/%s/server/jar", version, loader, installer), nil
}

var errNotFound = fmt.Errorf("not found")

// getJSON downloads a URL and reads the JSON answer into "into".
func getJSON(ctx context.Context, url string, into any) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(into)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed (is the server PC online?): %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		resp.Body.Close()
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: %s answered %s", req.URL.Host, resp.Status)
	}
	return resp, nil
}

// saveFile downloads url to path. If h and want are set, it checks the
// file's checksum so a broken or tampered download is never used.
func saveFile(ctx context.Context, url, path string, h hash.Hash, want string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var w io.Writer = f
	if h != nil {
		w = io.MultiWriter(f, h)
	}
	_, copyErr := io.Copy(w, resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("download interrupted: %v %v", copyErr, closeErr)
	}
	if h != nil && want != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			os.Remove(tmp)
			return fmt.Errorf("download was corrupted (checksum mismatch), try again")
		}
	}
	return os.Rename(tmp, path)
}
