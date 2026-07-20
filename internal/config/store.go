package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var ErrRevisionConflict = errors.New("configuration revision conflict")

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func DefaultPath() (string, error) {
	if path := os.Getenv("DOCKSTAR_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dockstar", "config.json"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	s := &Store{path: path, cfg: Default()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.write(s.cfg); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := s.cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cfg)
}

func (s *Store) Replace(next Config, expectedRevision uint64) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedRevision != s.cfg.Revision {
		return Config{}, fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, expectedRevision, s.cfg.Revision)
	}
	next.SchemaVersion = SchemaVersion
	next.Revision = s.cfg.Revision + 1
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	if err := s.write(next); err != nil {
		return Config{}, err
	}
	s.cfg = clone(next)
	return clone(next), nil
}

func (s *Store) Update(fn func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cfg)
	if err := fn(&next); err != nil {
		return Config{}, err
	}
	next.SchemaVersion = SchemaVersion
	next.Revision++
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	if err := s.write(next); err != nil {
		return Config{}, err
	}
	s.cfg = clone(next)
	return clone(next), nil
}

func (s *Store) Reload() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Config{}, err
	}
	var next Config
	if err := json.Unmarshal(data, &next); err != nil {
		return Config{}, err
	}
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	s.cfg = clone(next)
	return clone(next), nil
}

func (s *Store) write(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func clone(cfg Config) Config {
	cfg.Displays = append([]DisplayRule{}, cfg.Displays...)
	return cfg
}
