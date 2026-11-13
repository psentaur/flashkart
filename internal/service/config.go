package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
)

type config struct {
	InventoryGRPC      string            `json:"inventory_grpc_address"`
	Environment        string            `json:"environment"`
	HTTP               string            `json:"http_address"`
	GRPC               string            `json:"grpc_address"`
	Databases          map[string]string `json:"database_env"`
	ReplicaID          string            `json:"replica_id"`
	Peers              map[string]string `json:"peers"`
	PeerTimeoutMS      int64             `json:"peer_timeout_ms"`
	OperationTimeoutMS int64             `json:"operation_timeout_ms"`
	AWS                struct {
		Region    string            `json:"region"`
		Endpoints map[string]string `json:"endpoints"`
	} `json:"aws"`
	Kafka struct {
		Brokers []string `json:"brokers"`
		Auth    string   `json:"auth"`
	} `json:"kafka"`
}

func address(value string, allowZero bool) bool {
	_, port, err := net.SplitHostPort(value)
	n, numberErr := strconv.Atoi(port)
	return err == nil && numberErr == nil && n <= 65535 && (n > 0 || allowZero && n == 0)
}

func readConfig(path, name string) (config, error) {
	cfg := config{Environment: "local", HTTP: "127.0.0.1:8080", PeerTimeoutMS: 250, OperationTimeoutMS: 1000, InventoryGRPC: "127.0.0.1:7101"}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return cfg, err
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		value := &cfg
		if err := decoder.Decode(&value); err != nil {
			return cfg, err
		}
		if value == nil {
			return cfg, errors.New("expected a configuration object")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return cfg, errors.New("expected one configuration object")
		}
	}
	if cfg.Environment != "local" && cfg.Environment != "aws" {
		return cfg, errors.New("environment must be local or aws")
	}
	if !address(cfg.HTTP, true) || cfg.GRPC != "" && !address(cfg.GRPC, true) {
		return cfg, errors.New("invalid HTTP or gRPC listen address")
	}
	replicas := []string{"a", "b", "c", "d"}
	if len(cfg.Databases) != 0 {
		seen := map[string]bool{}
		for _, id := range append([]string{"transactional"}, replicas...) {
			variable := cfg.Databases[id]
			if variable == "" || seen[variable] {
				return cfg, errors.New("database_env requires five distinct environment variable names")
			}
			seen[variable] = true
		}
		if len(cfg.Databases) != 5 {
			return cfg, errors.New("unexpected database identity")
		}
	}
	if name == "catalog-replica" {
		if cfg.PeerTimeoutMS <= 0 || cfg.OperationTimeoutMS < cfg.PeerTimeoutMS || cfg.OperationTimeoutMS > int64((1<<63-1)/1_000_000) {
			return cfg, errors.New("replica deadlines require 0 < peer_timeout_ms <= operation_timeout_ms within the duration range")
		}
		if len(cfg.Peers) != 4 || len(cfg.Databases) != 5 || cfg.GRPC == "" || cfg.Peers[cfg.ReplicaID] == "" {
			return cfg, errors.New("catalog replica requires an identity, four peers, five database references and a gRPC listener")
		}
	}
	if len(cfg.Peers) != 0 {
		seen := map[string]bool{}
		for _, id := range replicas {
			peer := cfg.Peers[id]
			if !address(peer, false) || seen[peer] {
				return cfg, fmt.Errorf("invalid or repeated address for replica %s", id)
			}
			seen[peer] = true
		}
		if len(cfg.Peers) != 4 {
			return cfg, errors.New("unexpected replica identity")
		}
	}
	if cfg.Environment == "aws" && (cfg.AWS.Region == "" || len(cfg.AWS.Endpoints) != 0 || cfg.Kafka.Auth != "iam") {
		return cfg, errors.New("AWS configuration requires a region, standard AWS endpoints and Kafka IAM authentication")
	}
	if cfg.Kafka.Auth != "" && cfg.Kafka.Auth != "plaintext" && cfg.Kafka.Auth != "iam" {
		return cfg, errors.New("Kafka auth must be plaintext or iam")
	}
	for _, broker := range cfg.Kafka.Brokers {
		if !address(broker, false) {
			return cfg, errors.New("invalid Kafka broker address")
		}
	}
	for _, endpoint := range cfg.AWS.Endpoints {
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
			return cfg, errors.New("invalid local AWS endpoint")
		}
	}
	return cfg, nil
}
