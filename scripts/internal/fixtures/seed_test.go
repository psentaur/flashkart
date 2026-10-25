package fixtures

import (
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedRefreshPreservesDatasetIdentity(t *testing.T) {
	dir := t.TempDir()
	for k, v := range map[string]string{"JWT_PRIVATE_KEY_FILE": filepath.Join(dir, "private.pem"), "JWT_PUBLIC_KEY_FILE": filepath.Join(dir, "public.pem"), "SEEDED_TOKENS_FILE": filepath.Join(dir, "tokens.json"), "JWT_ISSUER": "test", "JWT_AUDIENCE": "flashkart"} {
		t.Setenv(k, v)
	}
	now := time.Now().UTC().Truncate(time.Second)
	type identity struct{ Subject, Role, Token string }
	read := func() []identity {
		raw, e := os.ReadFile(filepath.Join(dir, "tokens.json"))
		if e != nil {
			t.Fatal(e)
		}
		var manifest struct{ Identities []identity }
		if e = json.Unmarshal(raw, &manifest); e != nil {
			t.Fatal(e)
		}
		return manifest.Identities
	}
	if e := SeedAt(now); e != nil {
		t.Fatal(e)
	}
	before := read()
	if e := SeedAt(now.Add(20 * time.Minute)); e != nil {
		t.Fatal(e)
	}
	after := read()
	if len(before) != 101 || len(after) != 101 {
		t.Fatal("identity count")
	}
	v, e := auth.Load(filepath.Join(dir, "public.pem"), "test", "flashkart")
	if e != nil {
		t.Fatal(e)
	}
	v.Now = func() time.Time { return now.Add(20 * time.Minute) }
	for i, item := range after {
		if item.Subject != before[i].Subject || item.Role != before[i].Role {
			t.Fatal("identity replaced on refresh")
		}
		p, e := v.Verify(item.Token)
		if e != nil || p.Subject != item.Subject {
			t.Fatal("renewed claims invalid", e)
		}
	}
	for _, name := range []string{"private.pem", "tokens.json"} {
		info, e := os.Stat(filepath.Join(dir, name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private file mode", e)
		}
	}
}
