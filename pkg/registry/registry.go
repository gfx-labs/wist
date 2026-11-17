// Package registry fetches the ACP agent registry, caches it, and resolves
// an agent id to a runnable command, downloading and verifying binary
// distributions when needed.
package registry

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const registryURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// Registry is the parsed ACP registry document.
type Registry struct {
	Agents []Agent `json:"agents"`
}

// Agent is one registry entry.
type Agent struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Distribution Distribution `json:"distribution"`
}

// Distribution lists the ways an agent can be run.
type Distribution struct {
	Binary map[string]Binary `json:"binary,omitempty"`
	Npx    *Package          `json:"npx,omitempty"`
	Uvx    *Package          `json:"uvx,omitempty"`
}

// Binary is a downloadable archive for one platform.
type Binary struct {
	Archive string            `json:"archive"`
	Sha256  string            `json:"sha256"`
	Cmd     string            `json:"cmd"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// Package is an npx or uvx package distribution.
type Package struct {
	Package string            `json:"package"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// Command is how to start an agent.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string // set for downloaded binaries
	// fetch, when set, must run before the command exists on disk.
	fetch *Binary
}

func cacheDir() string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "wist")
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Load returns the registry, from a cache younger than an hour
// when possible. A stale cache is used if the network fails.
func Load() (*Registry, error) {
	p := filepath.Join(cacheDir(), "registry.json")
	st, statErr := os.Stat(p)
	if statErr == nil && time.Since(st.ModTime()) < time.Hour {
		if r, err := readRegistry(p); err == nil {
			return r, nil
		}
	}
	err := fetchTo(registryURL, p, 16<<20)
	if err != nil {
		if r, rerr := readRegistry(p); rerr == nil {
			return r, nil
		}
		return nil, fmt.Errorf("fetch registry: %w", err)
	}
	return readRegistry(p)
}

func readRegistry(p string) (*Registry, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r Registry
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return nil, err
	}
	sort.Slice(r.Agents, func(i, j int) bool { return r.Agents[i].ID < r.Agents[j].ID })
	return &r, nil
}

// fetchTo downloads url into path atomically, refusing bodies over limit.
func fetchTo(url, path string, limit int64) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dl-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return os.Rename(tmp.Name(), path)
}

func (r *Registry) find(id string) *Agent {
	for i := range r.Agents {
		if r.Agents[i].ID == id {
			return &r.Agents[i]
		}
	}
	return nil
}

// platformKey names this machine the way the registry does.
func platformKey(goos, goarch string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		arch = goarch
	}
	return goos + "-" + arch
}

func (a *Agent) kinds() string {
	var k []string
	if len(a.Distribution.Binary) > 0 {
		k = append(k, "binary")
	}
	if a.Distribution.Npx != nil {
		k = append(k, "npx")
	}
	if a.Distribution.Uvx != nil {
		k = append(k, "uvx")
	}
	return strings.Join(k, ",")
}

// resolve picks how to run the agent on goos/goarch. A binary for this
// platform is preferred, then npx, then uvx.
func (a *Agent) resolve(goos, goarch string) (Command, error) {
	d := a.Distribution
	if b, ok := d.Binary[platformKey(goos, goarch)]; ok && b.Archive != "" && b.Cmd != "" {
		dir := filepath.Join(cacheDir(), "agents", a.ID, a.Version)
		bin := filepath.Clean(filepath.Join(dir, b.Cmd))
		if !strings.HasPrefix(bin, dir+string(filepath.Separator)) {
			return Command{}, fmt.Errorf("%s: cmd %q escapes its directory", a.ID, b.Cmd)
		}
		c := Command{Path: bin, Args: b.Args, Env: envList(b.Env), Dir: dir}
		if !fileExists(bin) {
			bb := b
			c.fetch = &bb
		}
		return c, nil
	}
	if p := d.Npx; p != nil && p.Package != "" {
		return Command{Path: "npx", Args: append([]string{"-y", p.Package}, p.Args...), Env: envList(p.Env)}, nil
	}
	if p := d.Uvx; p != nil && p.Package != "" {
		return Command{Path: "uvx", Args: append([]string{p.Package}, p.Args...), Env: envList(p.Env)}, nil
	}
	return Command{}, fmt.Errorf("%s has no distribution for %s", a.ID, platformKey(goos, goarch))
}

func envList(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// install downloads, verifies and unpacks a binary distribution into dir.
func install(b *Binary, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	archive := filepath.Join(dir, ".archive")
	defer os.Remove(archive)
	if err := fetchTo(b.Archive, archive, 1<<30); err != nil {
		return err
	}
	if err := verifySha256(archive, b.Sha256); err != nil {
		return err
	}
	name := strings.ToLower(b.Archive)
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return untar(archive, dir)
	case strings.HasSuffix(name, ".zip"):
		return unzip(archive, dir)
	}
	// A bare executable.
	dst := filepath.Join(dir, filepath.Base(b.Cmd))
	if err := os.Rename(archive, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

func verifySha256(path, want string) error {
	if want == "" {
		return errors.New("registry entry has no sha256")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

// safeJoin joins an archive entry name to dir, rejecting path traversal.
func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, name)
	if p != dir && !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the target directory", name)
	}
	return p, nil
}

func untar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(p, tr, os.FileMode(h.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if _, err := safeJoin(dir, filepath.Join(filepath.Dir(h.Name), h.Linkname)); err != nil || filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("archive symlink %q escapes the target directory", h.Name)
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.Remove(p)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		}
	}
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		p, err := safeJoin(dir, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		mode := zf.Mode() & 0o777
		if mode == 0 {
			mode = 0o755
		}
		err = writeFile(p, rc, mode)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Resolve turns an agent id into a command. "towa" (or empty) is the
// built-in default; anything else is looked up in the registry and
// installed if needed. progress gets a line before slow steps.
func Resolve(id string, progress func(string)) (Command, error) {
	r, err := Load()
	if err != nil {
		return Command{}, err
	}
	a := r.find(id)
	if a == nil {
		return Command{}, fmt.Errorf("unknown agent %q (see `wist agents`)", id)
	}
	c, err := a.resolve(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return c, err
	}
	if c.fetch != nil {
		progress(fmt.Sprintf("installing %s %s into %s", a.ID, a.Version, c.Dir))
		if err := install(c.fetch, c.Dir); err != nil {
			_ = os.RemoveAll(c.Dir)
			return c, fmt.Errorf("install %s: %w", a.ID, err)
		}
		c.fetch = nil
		if !fileExists(c.Path) {
			return c, fmt.Errorf("install %s: %s not found in the archive", a.ID, c.Path)
		}
	}
	return c, nil
}

// List writes a table of the registry agents, towa first, to w.
func List(w io.Writer) error {
	r, err := Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%-22s %-10s %-12s %s\n", "towa", "(default)", "local", "towa, the default agent")
	for _, a := range r.Agents {
		fmt.Fprintf(w, "%-22s %-10s %-12s %s\n", a.ID, a.Version, a.kinds(), a.Description)
	}
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
