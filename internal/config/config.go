package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the top-level configuration structure.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	BGPLS    BGPLSConfig    `mapstructure:"bgpls"`
	PCEP     PCEPConfig     `mapstructure:"pcep"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Log      LogConfig      `mapstructure:"log"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	SSLMode  string `mapstructure:"ssl_mode"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode,
	)
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// BGPLSConfig configures how Controllore peers with GoBGP for BGP-LS.
type BGPLSConfig struct {
	// LocalAS is the ASN of the Controllore BGP speaker.
	LocalAS uint32 `mapstructure:"local_as"`
	// LocalAddr is the local bind address for BGP.
	LocalAddr string `mapstructure:"local_addr"`
	// RouterID is the BGP Router ID for the controller.
	RouterID string `mapstructure:"router_id"`
	// Peers is the list of BGP-LS peers (typically FRR instances).
	Peers []BGPPeerConfig `mapstructure:"peers"`
}

type BGPPeerConfig struct {
	Addr        string        `mapstructure:"addr"`
	AS          uint32        `mapstructure:"as"`
	Description string        `mapstructure:"description"`
	HoldTime    time.Duration `mapstructure:"hold_time"`
	AuthPassword string       `mapstructure:"auth_password"`
}

// PCEPConfig configures the PCEP server.
type PCEPConfig struct {
	// ListenAddr is the address to bind the PCEP TCP listener (port 4189).
	ListenAddr string `mapstructure:"listen_addr"`
	Port       int    `mapstructure:"port"`
	// Keepalive is the PCEP keepalive interval in seconds.
	Keepalive uint8 `mapstructure:"keepalive"`
	// DeadTimer is the PCEP dead timer in seconds (typically 4x keepalive).
	DeadTimer uint8 `mapstructure:"dead_timer"`
	// TLS enables TLS for PCEP sessions (RFC 8253).
	TLS     bool   `mapstructure:"tls"`
	TLSCert string `mapstructure:"tls_cert"`
	TLSKey  string `mapstructure:"tls_key"`
}

type AuthConfig struct {
	// JWTSecret is the HMAC secret for JWT token signing.
	JWTSecret string        `mapstructure:"jwt_secret"`
	TokenTTL  time.Duration `mapstructure:"token_ttl"`
	// Enabled controls whether auth is enforced (set false for lab/dev).
	Enabled bool `mapstructure:"enabled"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // "json" or "console"
}

// Load reads configuration from the given file path and environment variables.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.name", "controllore")
	v.SetDefault("database.user", "controllore")
	v.SetDefault("database.ssl_mode", "disable")
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)
	v.SetDefault("bgpls.local_as", 65000)
	v.SetDefault("bgpls.local_addr", "0.0.0.0")
	v.SetDefault("bgpls.router_id", "192.0.2.254")
	v.SetDefault("pcep.listen_addr", "0.0.0.0")
	v.SetDefault("pcep.port", 4189)
	v.SetDefault("pcep.keepalive", 30)
	v.SetDefault("pcep.dead_timer", 120)
	v.SetDefault("pcep.tls", false)
	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.token_ttl", "24h")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")

	// Environment variable overrides: CONTROLLORE_SERVER_PORT, etc.
	v.SetEnvPrefix("CONTROLLORE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Config file
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("controllore")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/controllore/")
		v.AddConfigPath("$HOME/.controllore/")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		// Config file not found; use defaults + env
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, nil
}
