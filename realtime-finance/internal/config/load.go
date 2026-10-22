package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// LoadStrategy reads strategy.toml over the defaults, so missing keys keep their default.
func LoadStrategy(path string) (*Strategy, error) {
	s := DefaultStrategy()
	if err := decodeStrict(path, &s); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func LoadMarket(path string) (*Market, error) {
	m := DefaultMarket()
	if err := decodeStrict(path, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadStock reads one stocks/<SYMBOL>.toml. The file name (without .toml) must match the symbol.
func LoadStock(path string) (*Stock, error) {
	var s Stock
	if err := decodeStrict(path, &s); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	want := strings.TrimSuffix(filepath.Base(path), ".toml")
	if !strings.EqualFold(want, s.Symbol) {
		return nil, fmt.Errorf("%s: symbol %q does not match file name", filepath.Base(path), s.Symbol)
	}
	return &s, nil
}

// LoadStocks reads every *.toml in dir. A bad file is reported in errs and skipped.
func LoadStocks(dir string) (stocks []*Stock, errs []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{err}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, n := range names {
		s, err := LoadStock(filepath.Join(dir, n))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[s.Symbol] {
			errs = append(errs, fmt.Errorf("%s: duplicate symbol %s", n, s.Symbol))
			continue
		}
		seen[s.Symbol] = true
		stocks = append(stocks, s)
	}
	return stocks, errs
}

func decodeStrict(path string, v any) error {
	md, err := toml.DecodeFile(path, v)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		keys := make([]string, len(undec))
		for i, k := range undec {
			keys[i] = k.String()
		}
		return fmt.Errorf("%s: unknown keys: %s", filepath.Base(path), strings.Join(keys, ", "))
	}
	return nil
}

// DorisEnv is the VeloDB connection: environment variables > .env (KEY=VALUE lines) > defaults. See env.example.
type DorisEnv struct {
	Host, User, Password, Database, StreamLoadURL string
	Port                                          int
	PricesRetentionHours                          int // 0 = keep forever
	SnapshotsRetentionDays                        int // 0 = keep forever
	TradesRetentionDays                           int // 0 = keep forever; otherwise old partitions are folded into carry rows, then dropped
}

var envKeys = []string{"VELODB_HOST", "VELODB_MYSQL_PORT", "VELODB_USER", "VELODB_PASSWORD", "VELODB_DATABASE",
	"VELODB_STREAM_LOAD_PORT", "VELODB_STREAM_LOAD_URL", "PRICES_RETENTION_HOURS", "SNAPSHOTS_RETENTION_DAYS", "TRADES_RETENTION_DAYS"}

// legacyKeys maps the key names of the old doris.env file to the current ones.
var legacyKeys = map[string]string{"MYSQL_HOST": "VELODB_HOST", "MYSQL_PORT": "VELODB_MYSQL_PORT", "MYSQL_USER": "VELODB_USER",
	"MYSQL_PASSWORD": "VELODB_PASSWORD", "DATABASE": "VELODB_DATABASE", "STREAM_LOAD_URL": "VELODB_STREAM_LOAD_URL"}

// LoadDorisEnv reads the file at path when it exists (a missing file means environment only), then applies the
// environment on top. Old doris.env key names (MYSQL_HOST, DATABASE, ...) are still accepted in the file.
func LoadDorisEnv(path string) (*DorisEnv, error) {
	vals := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	name := filepath.Base(path)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if nk, ok := legacyKeys[k]; ok {
			k = nk
		}
		vals[k] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	for _, k := range envKeys {
		if v := os.Getenv(k); v != "" {
			vals[k] = v
		}
	}
	e := &DorisEnv{
		Host: vals["VELODB_HOST"], User: vals["VELODB_USER"], Password: vals["VELODB_PASSWORD"],
		Database: vals["VELODB_DATABASE"], StreamLoadURL: strings.TrimRight(vals["VELODB_STREAM_LOAD_URL"], "/"), Port: 9030,
	}
	if e.Database == "" {
		e.Database = "money_market_demo"
	}
	if p := vals["VELODB_MYSQL_PORT"]; p != "" {
		if _, err := fmt.Sscanf(p, "%d", &e.Port); err != nil {
			return nil, fmt.Errorf("%s: bad VELODB_MYSQL_PORT %q", name, p)
		}
	}
	if e.StreamLoadURL == "" && e.Host != "" {
		port := vals["VELODB_STREAM_LOAD_PORT"]
		if port == "" {
			port = "8080"
		}
		e.StreamLoadURL = "http://" + e.Host + ":" + port
	}
	e.PricesRetentionHours, e.SnapshotsRetentionDays, e.TradesRetentionDays = 48, 7, 7
	if v := vals["PRICES_RETENTION_HOURS"]; v != "" {
		fmt.Sscanf(v, "%d", &e.PricesRetentionHours)
	}
	if v := vals["SNAPSHOTS_RETENTION_DAYS"]; v != "" {
		fmt.Sscanf(v, "%d", &e.SnapshotsRetentionDays)
	}
	if v := vals["TRADES_RETENTION_DAYS"]; v != "" {
		fmt.Sscanf(v, "%d", &e.TradesRetentionDays)
	}
	for k, v := range map[string]string{"VELODB_HOST": e.Host, "VELODB_USER": e.User} {
		if v == "" {
			return nil, fmt.Errorf("%s is not set - copy env.example to .env or export it (see README: Configure)", k)
		}
	}
	return e, nil
}
