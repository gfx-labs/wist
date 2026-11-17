package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryResolve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := Agent{ID: "demo", Version: "1.0", Distribution: Distribution{
		Binary: map[string]Binary{"linux-x86_64": {Archive: "https://x/demo.tar.gz", Sha256: "ab", Cmd: "./bin/demo", Args: []string{"acp"}}},
		Npx:    &Package{Package: "@demo/acp", Args: []string{"--acp"}},
	}}
	c, err := a.resolve("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if c.fetch == nil || filepath.Base(c.Path) != "demo" || !strings.Contains(c.Path, filepath.Join("agents", "demo", "1.0")) {
		t.Fatalf("%+v", c)
	}
	c, _ = a.resolve("darwin", "arm64")
	if c.Path != "npx" || strings.Join(c.Args, " ") != "-y @demo/acp --acp" {
		t.Fatalf("%+v", c)
	}
	a.Distribution.Npx = nil
	a.Distribution.Uvx = &Package{Package: "demo-acp"}
	if c, _ = a.resolve("windows", "amd64"); c.Path != "uvx" {
		t.Fatalf("%+v", c)
	}
	a.Distribution.Uvx = nil
	if _, err := a.resolve("plan9", "386"); err == nil {
		t.Fatal("no distribution should fail")
	}
	a.Distribution.Binary["linux-x86_64"] = Binary{Archive: "x", Cmd: "../../evil"}
	if _, err := a.resolve("linux", "amd64"); err == nil {
		t.Fatal("cmd escaping the install dir should fail")
	}
}

func TestRegistryParseAndVerify(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	os.WriteFile(p, []byte(`{"version":"1","agents":[{"id":"b","name":"B","version":"2","description":"d","distribution":{"uvx":{"package":"b"}}},{"id":"a","name":"A","version":"1","distribution":{"npx":{"package":"a"}}}]}`), 0o644)
	r, err := readRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Agents[0].ID != "a" || r.find("b") == nil || r.find("zz") != nil || r.find("b").kinds() != "uvx" {
		t.Fatalf("%+v", r)
	}
	f := filepath.Join(dir, "f")
	os.WriteFile(f, []byte("hello"), 0o644)
	if err := verifySha256(f, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Fatal(err)
	}
	if verifySha256(f, "00") == nil || verifySha256(f, "") == nil {
		t.Fatal("bad hash should fail")
	}
	if _, err := safeJoin(dir, "../x"); err == nil {
		t.Fatal("traversal should fail")
	}
}
