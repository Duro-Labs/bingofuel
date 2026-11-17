// Package secrets stores API keys in the OS keyring (Secret Service, macOS
// Keychain, Windows Credential Manager), falling back to a 0600 file in the
// config dir when no keyring is reachable (headless session, no D-Bus).
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fernandoduro/bingofuel/internal/config"
	"github.com/zalando/go-keyring"
)

const service = "bingofuel"

// Source names where a secret came from; it never contains the secret.
type Source string

const (
	SourceKeyring Source = "keyring"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceNone    Source = ""
)

var ErrNotFound = errors.New("no key stored")

// keyringTimeout bounds every keyring call: a locked or absent Secret
// Service must degrade to the file fallback, not hang the widget.
const keyringTimeout = 3 * time.Second

func withTimeout[T any](f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() { v, err := f(); ch <- res{v, err} }()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, errors.New("keyring did not answer")
	}
}

// Get returns the key for a provider (e.g. "openrouter"): keyring, then the
// fallback file, then the environment variable envName.
func Get(provider, envName string) (string, Source, error) {
	if v, err := withTimeout(func() (string, error) { return keyring.Get(service, provider) }); err == nil && v != "" {
		return v, SourceKeyring, nil
	}
	if v, err := fileGet(provider); err == nil && v != "" {
		return v, SourceFile, nil
	}
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			return v, SourceEnv, nil
		}
	}
	return "", SourceNone, ErrNotFound
}

// Set stores the key in the keyring, or in the fallback file if the keyring
// is unavailable. It reports where the key went.
func Set(provider, value string) (Source, error) {
	if _, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Set(service, provider, value) }); err == nil {
		_ = fileDelete(provider) // one source of truth
		return SourceKeyring, nil
	}
	if err := fileSet(provider, value); err != nil {
		return SourceNone, err
	}
	return SourceFile, nil
}

func Delete(provider string) {
	_, _ = withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Delete(service, provider) })
	_ = fileDelete(provider)
}

// KeyringAvailable probes the keyring without storing anything.
func KeyringAvailable() error {
	_, err := withTimeout(func() (string, error) { return keyring.Get(service, "__probe__") })
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func filePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.toml"), nil
}

func fileLoad() (map[string]string, string, error) {
	p, err := filePath()
	if err != nil {
		return nil, "", err
	}
	m := map[string]string{}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return m, p, nil
	}
	if err != nil {
		return nil, p, err
	}
	if _, err := toml.Decode(string(data), &m); err != nil {
		// Never wrap the decoder error: it can quote the file's contents.
		return nil, p, fmt.Errorf("%s is not valid TOML", p)
	}
	return m, p, nil
}

func fileGet(provider string) (string, error) {
	m, _, err := fileLoad()
	if err != nil {
		return "", err
	}
	return m[provider], nil
}

func fileSet(provider, value string) error {
	m, p, err := fileLoad()
	if err != nil {
		return err
	}
	m[provider] = value
	return fileWrite(p, m)
}

func fileDelete(provider string) error {
	m, p, err := fileLoad()
	if err != nil || m[provider] == "" {
		return err
	}
	delete(m, provider)
	if len(m) == 0 {
		return os.Remove(p)
	}
	return fileWrite(p, m)
}

func fileWrite(p string, m map[string]string) error {
	var b []byte
	buf := &byteBuf{b}
	buf.WriteString("# Bingo Fuel API keys (fallback when no OS keyring is available). Keep private.\n")
	if err := toml.NewEncoder(buf).Encode(m); err != nil {
		return err
	}
	return config.WriteFileAtomic(p, buf.b, 0o600)
}

type byteBuf struct{ b []byte }

func (w *byteBuf) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *byteBuf) WriteString(s string)        { w.b = append(w.b, s...) }
