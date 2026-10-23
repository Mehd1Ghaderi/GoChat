package scylla

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/utils"
)

type Config struct {
	Hosts             []string      `mapstructure:"hosts"`
	Keyspace          string        `mapstructure:"keyspace"`
	Username          string        `mapstructure:"username"`
	Password          string        `mapstructure:"password"`
	LocalDC           string        `mapstructure:"local_dc"`
	Consistency       string        `mapstructure:"consistency"`
	SerialConsistency string        `mapstructure:"serial_consistency"`
	Timeout           time.Duration `mapstructure:"timeout"`
	ConnectTimeout    time.Duration `mapstructure:"connect_timeout"`
	NumConns          int           `mapstructure:"num_conns"`
	// DisableHostLookup talks only to Hosts, for a node whose advertised
	// address the client cannot reach (a container seen from its host).
	DisableHostLookup bool `mapstructure:"disable_host_lookup"`
	// Replication is the keyspace's replication map, used only when the
	// migrate command creates the keyspace.
	Replication string     `mapstructure:"replication"`
	TLS         *TLSConfig `mapstructure:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CAFile   string `mapstructure:"ca_file"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if len(c.Hosts) == 0 {
		return errors.New("hosts is required")
	}
	for _, h := range c.Hosts {
		if err := utils.ValidateAddress(h); err != nil {
			return fmt.Errorf("hosts: %s: %w", h, err)
		}
	}
	if !validIdentifier(c.Keyspace) {
		return errors.New("keyspace must be a lowercase identifier")
	}
	if _, err := parseConsistency(c.Consistency); err != nil {
		return fmt.Errorf("consistency: %w", err)
	}
	if _, err := parseSerial(c.SerialConsistency); err != nil {
		return fmt.Errorf("serial_consistency: %w", err)
	}
	if c.Timeout <= 0 || c.ConnectTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	if c.NumConns < 1 {
		return errors.New("num_conns must be at least 1")
	}
	if !strings.HasPrefix(strings.TrimSpace(c.Replication), "{") {
		return errors.New("replication must be a CQL map, such as {'class': 'NetworkTopologyStrategy', 'dc1': 3}")
	}
	if c.TLS == nil {
		return errors.New("tls is required")
	}
	if c.TLS.Enabled && (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return errors.New("tls.cert_file and tls.key_file go together")
	}
	return nil
}

func validIdentifier(s string) bool {
	if s == "" || len(s) > 48 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func parseConsistency(s string) (gocql.Consistency, error) {
	switch s {
	case "one":
		return gocql.One, nil
	case "local_one":
		return gocql.LocalOne, nil
	case "quorum":
		return gocql.Quorum, nil
	case "local_quorum":
		return gocql.LocalQuorum, nil
	case "each_quorum":
		return gocql.EachQuorum, nil
	case "all":
		return gocql.All, nil
	default:
		return 0, fmt.Errorf("unknown consistency %q", s)
	}
}

func parseSerial(s string) (gocql.Consistency, error) {
	switch s {
	case "serial":
		return gocql.Serial, nil
	case "local_serial":
		return gocql.LocalSerial, nil
	default:
		return 0, fmt.Errorf("unknown serial consistency %q", s)
	}
}
