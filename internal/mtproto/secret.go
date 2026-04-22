// Package mtproto integrates the 9seconds/mtg MTProto FakeTLS proxy library
// into dyndns as a library, so port 443 can host both browser/HTTPS traffic
// (forwarded to Caddy) and MTProto sessions for selected direct-mode
// subdomains.
//
// This file deals with per-subdomain secrets: generate, load, persist,
// validate, and render them as tg:// links.
package mtproto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/9seconds/mtg/v2/mtglib"
)

const (
	// SecretFileMode restricts the on-disk secret to the owner.
	SecretFileMode = 0o600
	// secretPrefix is the FakeTLS marker mtg requires at the start of the secret.
	secretPrefix = 0xee
)

// Binding pairs a subdomain with its resolved FQDN and secret material.
type Binding struct {
	// Subdomain is the label as configured (e.g., "mtp").
	Subdomain string
	// FQDN is the fully-qualified hostname (e.g., "mtp.zone33.example.com").
	FQDN string
	// Secret is the mtglib Secret (key + host).
	Secret mtglib.Secret
}

// SecretBytes returns the serialized secret bytes (0xee || key || fqdn).
func (b Binding) SecretBytes() []byte {
	out := make([]byte, 0, 1+mtglib.SecretKeyLength+len(b.FQDN))
	out = append(out, secretPrefix)
	out = append(out, b.Secret.Key[:]...)
	out = append(out, []byte(b.Secret.Host)...)
	return out
}

// SecretHex returns the hex encoding of SecretBytes — the canonical "ee..." form
// that Telegram clients accept.
func (b Binding) SecretHex() string {
	return hex.EncodeToString(b.SecretBytes())
}

// Fingerprint returns a short stable identifier for the secret (first 8 hex
// chars of SHA-256 over the secret bytes). Safe to display in logs and /status.
func (b Binding) Fingerprint() string {
	sum := sha256.Sum256(b.SecretBytes())
	return hex.EncodeToString(sum[:])[:8]
}

// TelegramURL renders the tg:// proxy link that Telegram clients can import.
func (b Binding) TelegramURL() string {
	q := url.Values{}
	q.Set("server", b.FQDN)
	q.Set("port", "443")
	q.Set("secret", b.SecretHex())
	return "tg://proxy?" + q.Encode()
}

// Store is an on-disk store of generated secrets. Each subdomain has:
//
//	<dir>/<subdomain>.secret   — hex-encoded secret (one line, mode 0600)
//	<dir>/<subdomain>.tg       — full tg:// link (one line)
//
// The store is safe to call from a single goroutine at startup; it does not
// itself guard against concurrent mutation, because dyndns only reconciles
// the list from one place.
type Store struct {
	Dir string
}

// NewStore returns a store backed by the given directory. The directory is
// created (0700) if it does not exist.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("mtproto secret store: directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mtproto secret store: mkdir %q: %w", dir, err)
	}
	return &Store{Dir: dir}, nil
}

// Load returns the binding for the subdomain. If no secret exists yet, a
// fresh 16-byte key is generated, the Secret is built for the FQDN, and the
// pair is persisted. Existing secrets whose embedded host disagrees with the
// supplied fqdn are rejected with an error (caller should delete the .secret
// file and try again to rotate).
func (s *Store) Load(subdomain, fqdn string) (Binding, error) {
	if subdomain == "" {
		return Binding{}, fmt.Errorf("mtproto: subdomain is required")
	}
	if fqdn == "" {
		return Binding{}, fmt.Errorf("mtproto: fqdn is required")
	}

	path := filepath.Join(s.Dir, subdomain+".secret")
	existing, err := os.ReadFile(path)
	if err == nil {
		text := strings.TrimSpace(string(existing))
		var secret mtglib.Secret
		if err := secret.Set(text); err != nil {
			return Binding{}, fmt.Errorf("mtproto: existing secret at %s is invalid: %w", path, err)
		}
		if !strings.EqualFold(secret.Host, fqdn) {
			return Binding{}, fmt.Errorf(
				"mtproto: existing secret at %s encodes host %q but subdomain resolves to %q; delete the file to rotate",
				path, secret.Host, fqdn)
		}
		return Binding{Subdomain: subdomain, FQDN: fqdn, Secret: secret}, nil
	}
	if !os.IsNotExist(err) {
		return Binding{}, fmt.Errorf("mtproto: read %s: %w", path, err)
	}

	// Generate fresh material.
	secret, err := generateSecret(fqdn)
	if err != nil {
		return Binding{}, err
	}
	b := Binding{Subdomain: subdomain, FQDN: fqdn, Secret: secret}
	if err := s.persist(b); err != nil {
		return Binding{}, err
	}
	return b, nil
}

// Rotate unconditionally replaces the stored secret for the subdomain.
// Returns the new Binding. The caller is responsible for reloading any
// mtglib.Proxy instances that used the prior secret (or restarting the
// service — whichever is acceptable).
func (s *Store) Rotate(subdomain, fqdn string) (Binding, error) {
	if subdomain == "" || fqdn == "" {
		return Binding{}, fmt.Errorf("mtproto: subdomain and fqdn are required")
	}
	secret, err := generateSecret(fqdn)
	if err != nil {
		return Binding{}, err
	}
	b := Binding{Subdomain: subdomain, FQDN: fqdn, Secret: secret}
	if err := s.persist(b); err != nil {
		return Binding{}, err
	}
	return b, nil
}

func (s *Store) persist(b Binding) error {
	secretPath := filepath.Join(s.Dir, b.Subdomain+".secret")
	if err := writeFileAtomic(secretPath, []byte(b.SecretHex()+"\n"), SecretFileMode); err != nil {
		return fmt.Errorf("mtproto: write %s: %w", secretPath, err)
	}

	tgPath := filepath.Join(s.Dir, b.Subdomain+".tg")
	if err := writeFileAtomic(tgPath, []byte(b.TelegramURL()+"\n"), SecretFileMode); err != nil {
		return fmt.Errorf("mtproto: write %s: %w", tgPath, err)
	}
	return nil
}

func generateSecret(fqdn string) (mtglib.Secret, error) {
	var key [mtglib.SecretKeyLength]byte
	if _, err := rand.Read(key[:]); err != nil {
		return mtglib.Secret{}, fmt.Errorf("mtproto: entropy: %w", err)
	}
	return mtglib.Secret{Key: key, Host: fqdn}, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
