package mtproto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/9seconds/mtg/v2/mtglib"
)

func TestStore_Load_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	fqdn := "mtp.example.com"
	b, err := store.Load("mtp", fqdn)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.FQDN != fqdn {
		t.Errorf("FQDN = %q, want %q", b.FQDN, fqdn)
	}
	if !b.Secret.Valid() {
		t.Fatalf("secret invalid: %+v", b.Secret)
	}
	if b.Secret.Host != fqdn {
		t.Errorf("secret.Host = %q, want %q", b.Secret.Host, fqdn)
	}
	if b.SecretHex() == "" || !strings.HasPrefix(b.SecretHex(), "ee") {
		t.Errorf("SecretHex must start with ee: %q", b.SecretHex())
	}

	// A second Load returns the same persisted secret.
	b2, err := store.Load("mtp", fqdn)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if b2.SecretHex() != b.SecretHex() {
		t.Errorf("secret changed across loads: got %q, want %q", b2.SecretHex(), b.SecretHex())
	}

	// Persisted files exist and contain the expected content.
	secretBytes, err := os.ReadFile(filepath.Join(dir, "mtp.secret"))
	if err != nil {
		t.Fatalf("read secret: %v", err)
	}
	if strings.TrimSpace(string(secretBytes)) != b.SecretHex() {
		t.Errorf("on-disk secret = %q, want %q", secretBytes, b.SecretHex())
	}

	tgBytes, err := os.ReadFile(filepath.Join(dir, "mtp.tg"))
	if err != nil {
		t.Fatalf("read tg: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(tgBytes)), "tg://proxy?") {
		t.Errorf("tg file contents unexpected: %q", tgBytes)
	}
}

func TestStore_Load_RejectsHostMismatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Seed a secret for fqdn "a.example.com" then Load with "b.example.com".
	seed := mtglib.Secret{Host: "a.example.com"}
	for i := range seed.Key {
		seed.Key[i] = byte(i + 1)
	}
	seedPath := filepath.Join(dir, "mtp.secret")
	if err := os.WriteFile(seedPath, []byte(seed.String()+"\n"), SecretFileMode); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := store.Load("mtp", "b.example.com"); err == nil {
		t.Fatal("expected error for host mismatch, got nil")
	}
}

func TestStore_Rotate_ProducesFreshSecret(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	b1, err := store.Load("mtp", "mtp.example.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b2, err := store.Rotate("mtp", "mtp.example.com")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if b1.SecretHex() == b2.SecretHex() {
		t.Fatalf("rotate produced identical secret: %q", b1.SecretHex())
	}
	// On-disk file should reflect the rotated secret.
	raw, err := os.ReadFile(filepath.Join(dir, "mtp.secret"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(raw)) != b2.SecretHex() {
		t.Errorf("on-disk secret did not update after rotate")
	}
}

func TestBinding_TelegramURL(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	b, err := store.Load("mtp", "mtp.example.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	url := b.TelegramURL()
	if !strings.HasPrefix(url, "tg://proxy?") {
		t.Fatalf("tg url has wrong scheme: %q", url)
	}
	for _, must := range []string{"server=mtp.example.com", "port=443", "secret=" + b.SecretHex()} {
		if !strings.Contains(url, must) {
			t.Errorf("tg url missing %q: %q", must, url)
		}
	}
}

func TestBinding_Fingerprint_Stable(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	b, _ := store.Load("mtp", "mtp.example.com")
	if got := b.Fingerprint(); len(got) != 8 {
		t.Errorf("fingerprint length = %d, want 8 (value %q)", len(got), got)
	}
	if b.Fingerprint() != b.Fingerprint() {
		t.Error("fingerprint not stable")
	}
}
