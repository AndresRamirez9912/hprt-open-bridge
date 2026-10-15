package app

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/spf13/viper"
)

// envPrefix is the prefix of the environment variables that override config.yaml
const envPrefix = "BRIDGE"

// AppConfig contains all app configuration
type AppConfig struct {
	// App contains the identity, address and shutdown settings of the service
	App struct {
		// Name is the name of the application
		Name string `mapstructure:"name"`
		// ShutdownTimeout is the duration to wait before forcefully shutting down the application
		ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
		// Env is the environment where the application is running
		Env string `mapstructure:"env"`
		// RestAddr is the address for the REST server to listen on
		RestAddr string `mapstructure:"rest_addr"`
	} `mapstructure:"app"`

	// Logger contains the logger configuration
	Logger struct {
		Level   string `mapstructure:"level"`
		AsJSON  bool   `mapstructure:"as_json"`
		NoColor bool   `mapstructure:"no_color"`
	} `mapstructure:"logger"`

	// CORS contains the CORS configuration
	CORS struct {
		// Enabled determines if CORS is enabled
		Enabled bool `mapstructure:"enabled"`
		// AllowedOrigins is a list of allowed origins
		AllowedOrigins []string `mapstructure:"allowed_origins"`
		// AllowedMethods is a list of allowed HTTP methods
		AllowedMethods []string `mapstructure:"allowed_methods"`
		// AllowedHeaders is a list of allowed headers
		AllowedHeaders []string `mapstructure:"allowed_headers"`
	} `mapstructure:"cors"`

	// Printer contains the network ports and timings of the printer protocol
	Printer struct {
		// DataPort, CommandPort and DiscoveryPort only change to talk to a simulator
		DataPort      int `mapstructure:"data_port"`
		CommandPort   int `mapstructure:"command_port"`
		DiscoveryPort int `mapstructure:"discovery_port"`
		// WakeWindow is how long a connection keeps retrying while the printer wakes up
		WakeWindow time.Duration `mapstructure:"wake_window"`
		// HeartbeatInterval and HeartbeatTimeout keep the session alive and detect a dead link
		HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`
		HeartbeatTimeout  time.Duration `mapstructure:"heartbeat_timeout"`
		// ClaimTimeout bounds the wait for the print reservation
		ClaimTimeout time.Duration `mapstructure:"claim_timeout"`
		// PrintTimeout bounds a whole print job
		PrintTimeout time.Duration `mapstructure:"print_timeout"`
		// SendStall gives up sending the image after this long without progress
		SendStall time.Duration `mapstructure:"send_stall"`
		// SilentPrintDone ends a job as unconfirmed when the printer reports nothing
		SilentPrintDone time.Duration `mapstructure:"silent_print_done"`
		// ReconnectPause is the wait between two reconnection attempts
		ReconnectPause time.Duration `mapstructure:"reconnect_pause"`
		// RetryWait is how long an interrupted job waits for the reconnection before failing
		RetryWait time.Duration `mapstructure:"retry_wait"`
	} `mapstructure:"printer"`

	// Activity contains the protocol log configuration
	Activity struct {
		// LogBuffer is how many protocol log lines are kept in memory
		LogBuffer int `mapstructure:"log_buffer"`
	} `mapstructure:"activity"`
}

// LoadConfig initialize the configuration and env vars from the config files
func LoadConfig(path string) (*AppConfig, error) {
	// export env vars from .env file
	err := godotenv.Load(filepath.Join(path, ".env"))
	if err != nil {
		log.Println("Cannot load .env file, skipping...")
	}

	vip := viper.New()

	// Read config file
	vip.SetConfigName("config")
	vip.SetConfigType("yaml")
	vip.AddConfigPath(path)
	if err := vip.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("cannot read config.yaml: %w", err)
	}

	// Every key of config.yaml can be overridden by an env var, app.rest_addr is BRIDGE_APP_REST_ADDR
	vip.SetEnvPrefix(envPrefix)
	vip.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	vip.AutomaticEnv()

	// Load config and env vars on a AppConfig instance
	cfg := &AppConfig{}
	if err := vip.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("invalid config format: %w", err)
	}

	return cfg, nil
}
