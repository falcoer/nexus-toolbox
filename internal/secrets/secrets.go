// Package secrets stores credentials in the OS keyring, with a 0600 file fallback.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/zalando/go-keyring"
)

const service = "nexus-toolbox"

type Creds struct{ User, Password string }

func envKey(alias, field string) string {
	r := strings.NewReplacer("-", "_", ".", "_")
	return "NEXUS_" + strings.ToUpper(r.Replace(alias)) + "_" + field
}

// Get resolves credentials: environment first, then keyring, then fallback file.
// user is the login recorded in the config (used as the keyring account).
func Get(alias, user string) (Creds, error) {
	if p := os.Getenv(envKey(alias, "PASSWORD")); p != "" {
		u := os.Getenv(envKey(alias, "USER"))
		if u == "" {
			u = user
		}
		return Creds{u, p}, nil
	}
	if p, err := keyring.Get(service, alias); err == nil {
		return Creds{user, p}, nil
	}
	m, _ := readFile()
	if p, ok := m[alias]; ok {
		return Creds{user, p}, nil
	}
	return Creds{}, fmt.Errorf("aucun mot de passe enregistré pour %q — relancez `nexus init %s <url>`", alias, alias)
}

// Set stores the password; the returned bool is true when the insecure file fallback was used.
func Set(alias, password string) (fallback bool, err error) {
	if err = keyring.Set(service, alias, password); err == nil {
		return false, nil
	}
	m, _ := readFile()
	if m == nil {
		m = map[string]string{}
	}
	m[alias] = password
	return true, writeFile(m)
}

func Delete(alias string) {
	_ = keyring.Delete(service, alias)
	if m, _ := readFile(); m != nil {
		delete(m, alias)
		_ = writeFile(m)
	}
}

func filePath() (string, error) {
	d, err := config.Dir()
	return filepath.Join(d, "credentials"), err
}

func readFile() (map[string]string, error) {
	p, err := filePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	return m, json.Unmarshal(b, &m)
}

func writeFile(m map[string]string) error {
	p, err := filePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	return os.WriteFile(p, b, 0o600)
}
