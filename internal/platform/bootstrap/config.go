// Package bootstrap supplies only the shared Phase 1 startup shell.
// Domain behavior, auth middleware and dependency readiness arrive in later phases.
package bootstrap

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Component       string
	ListenAddress   string
	ShutdownTimeout time.Duration
}

// Load validates configuration without printing or retaining credential values.
func Load(component string) (Config, error) {
	ports := map[string]string{"admission": "8081", "flash-sale": "8082", "order": "8083", "analytics": "8084", "gateway": "8080"}
	port, ok := ports[component]
	if !ok {
		return Config{}, fmt.Errorf("unknown component")
	}
	environment := os.Getenv("APP_ENV")
	if environment == "" {
		environment = "development"
	}
	if environment != "development" && environment != "test" {
		return Config{}, fmt.Errorf("phase 1 startup supports APP_ENV=development or test only")
	}
	prefix := strings.ToUpper(strings.ReplaceAll(component, "-", "_"))
	address := os.Getenv(prefix + "_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:" + port
	}
	host, value, err := net.SplitHostPort(address)
	if err != nil {
		return Config{}, fmt.Errorf("%s_LISTEN_ADDR must be host:port", prefix)
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > 65535 || host == "" {
		return Config{}, fmt.Errorf("%s_LISTEN_ADDR must have a host and port 1..65535", prefix)
	}
	shutdown := 5 * time.Second
	if raw := os.Getenv("SHUTDOWN_TIMEOUT"); raw != "" {
		shutdown, err = time.ParseDuration(raw)
		if err != nil || shutdown <= 0 || shutdown > 30*time.Second {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be greater than zero and at most 30s")
		}
	}
	if component == "admission" || component == "flash-sale" || component == "order" {
		key := prefix + "_DATABASE_URL"
		parsed, err := url.Parse(os.Getenv(key))
		if err != nil || parsed == nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" || strings.Trim(parsed.Path, "/") == "" || parsed.User == nil {
			return Config{}, fmt.Errorf("%s must be a PostgreSQL URL with host, database and credentials", key)
		}
		password, present := parsed.User.Password()
		if parsed.User.Username() == "" || !present || password == "" {
			return Config{}, fmt.Errorf("%s requires a nonempty username and password", key)
		}
	}
	if component != "gateway" {
		if err := brokers(os.Getenv("KAFKA_BROKERS")); err != nil {
			return Config{}, err
		}
	} else {
		for _, key := range []string{"ADMISSION_URL", "FLASH_SALE_URL", "ORDER_URL", "CATALOG_URL", "NOTIFICATION_URL"} {
			parsed, err := url.Parse(os.Getenv(key))
			if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return Config{}, fmt.Errorf("%s must be an HTTP(S) URL without credentials, query or fragment", key)
			}
		}
	}
	return Config{component, address, shutdown}, nil
}

func brokers(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("KAFKA_BROKERS is required")
	}
	for _, address := range strings.Split(value, ",") {
		host, port, err := net.SplitHostPort(strings.TrimSpace(address))
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || host == "" || number < 1 || number > 65535 {
			return fmt.Errorf("KAFKA_BROKERS must contain host:port addresses")
		}
	}
	return nil
}
