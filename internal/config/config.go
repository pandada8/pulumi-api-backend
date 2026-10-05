package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Listen, PublicURL, ConsoleURL, DatabaseURL, KeyFile string
	Master                                              []byte
	Journal, DevHTTP                                    bool
}

func Load() (Config, error) {
	c := Config{Listen: os.Getenv("BACKEND_LISTEN"), PublicURL: strings.TrimRight(os.Getenv("BACKEND_PUBLIC_URL"), "/"), ConsoleURL: strings.TrimRight(os.Getenv("BACKEND_CONSOLE_URL"), "/"), DatabaseURL: os.Getenv("BACKEND_DATABASE_URL"), KeyFile: os.Getenv("BACKEND_MASTER_KEY_FILE"), Journal: os.Getenv("BACKEND_ENABLE_JOURNAL") == "true", DevHTTP: os.Getenv("BACKEND_DEV_HTTP") == "true"}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.ConsoleURL == "" {
		c.ConsoleURL = c.PublicURL
	}
	if c.DatabaseURL == "" || c.KeyFile == "" || c.PublicURL == "" {
		return c, errors.New("BACKEND_DATABASE_URL, BACKEND_PUBLIC_URL and BACKEND_MASTER_KEY_FILE are required")
	}
	for _, s := range []string{c.PublicURL, c.ConsoleURL} {
		u, e := url.Parse(s)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Path != "" || (u.Scheme != "https" && !(c.DevHTTP && u.Scheme == "http")) {
			return c, errors.New("invalid public/console URL (HTTP requires BACKEND_DEV_HTTP=true)")
		}
	}
	var e error
	c.Master, e = os.ReadFile(c.KeyFile)
	if e != nil {
		return c, errors.New("cannot read master key")
	}
	f, e := os.Stat(c.KeyFile)
	if e != nil || f.Mode().Perm()&0077 != 0 || len(c.Master) != 32 {
		return c, errors.New("master key must contain 32 bytes with mode 0600")
	}
	if os.Getenv("BACKEND_ENABLE_DELTA") == "true" {
		return c, errors.New("delta is not implemented; keep BACKEND_ENABLE_DELTA=false")
	}
	return c, nil
}
