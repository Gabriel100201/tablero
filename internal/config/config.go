package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
}

type ProviderConfig struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`     // "linear" or "taiga"
	APIKey   string `yaml:"api_key"`  // Linear
	URL      string `yaml:"url"`      // Taiga base URL
	Username string `yaml:"username"` // Taiga
	Password string `yaml:"password"` // Taiga
}

// Load reads, parses, and validates the config file at Path().
func Load() (*Config, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config found at %s — run `tablero config init` to create one, or `tablero config add linear` to add a provider interactively", path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadOrEmpty returns the existing config or an empty one if the file is missing.
// Used by CLI commands that need to mutate the config (add/remove) without failing
// when no config exists yet.
func LoadOrEmpty() (*Config, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config to Path(), creating parent directories if needed.
// On Unix-like systems the file is written with mode 0600 (owner-only) since it
// contains API keys and passwords.
func (c *Config) Save() error {
	if err := c.validate(); err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Path returns the resolved config file path (TABLERO_CONFIG > ~/.tablero/config.yaml).
func Path() string {
	if p := os.Getenv("TABLERO_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tablero/config.yaml"
	}
	return filepath.Join(home, ".tablero", "config.yaml")
}

// AddProvider appends a provider or returns an error if the name is already taken.
func (c *Config) AddProvider(p ProviderConfig) error {
	for _, existing := range c.Providers {
		if existing.Name == p.Name {
			return fmt.Errorf("provider %q already exists", p.Name)
		}
	}
	c.Providers = append(c.Providers, p)
	return nil
}

// RemoveProvider deletes a provider by name. Returns an error if it doesn't exist.
func (c *Config) RemoveProvider(name string) error {
	for i, p := range c.Providers {
		if p.Name == name {
			c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

func (c *Config) validate() error {
	names := make(map[string]bool)
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("provider[%d]: name is required", i)
		}
		if names[p.Name] {
			return fmt.Errorf("provider[%d]: duplicate name %q", i, p.Name)
		}
		names[p.Name] = true

		switch p.Type {
		case "linear":
			if p.APIKey == "" {
				return fmt.Errorf("provider %q (linear): api_key is required", p.Name)
			}
		case "taiga":
			if p.URL == "" {
				return fmt.Errorf("provider %q (taiga): url is required", p.Name)
			}
			if p.Username == "" || p.Password == "" {
				return fmt.Errorf("provider %q (taiga): username and password are required", p.Name)
			}
		default:
			return fmt.Errorf("provider %q: unknown type %q (must be 'linear' or 'taiga')", p.Name, p.Type)
		}
	}
	return nil
}
