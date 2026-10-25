// Command dev runs only Mukthish's foundation lifecycle and explicit fixture mode.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/postgres"
	"flash-sale/scripts/internal/fixtures"
	admission "flash-sale/services/admission"
	flashsale "flash-sale/services/flash-sale"
	"fmt"
	"github.com/IBM/sarama"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func run(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func localTopics(ctx context.Context) error {
	if os.Getenv("DEV_PEER_MODE") != "fixture" {
		return fmt.Errorf("local topics require fixture mode; shared deployment remains external")
	}
	raw, e := os.ReadFile("dev/topics.json")
	if e != nil {
		return e
	}
	var settings struct {
		Topics            []string
		Partitions        int32
		ReplicationFactor int16 `json:"replication_factor"`
		Config            map[string]*string
	}
	if json.Unmarshal(raw, &settings) != nil || settings.Partitions != 6 || settings.ReplicationFactor != 1 {
		return fmt.Errorf("invalid local topic settings")
	}
	admin, e := sarama.NewClusterAdmin(strings.Split(os.Getenv("KAFKA_BROKERS"), ","), messaging.Config("local-topic-setup"))
	if e != nil {
		return e
	}
	defer admin.Close()
	for _, name := range settings.Topics {
		if e := ctx.Err(); e != nil {
			return e
		}
		e = admin.CreateTopic(name, &sarama.TopicDetail{NumPartitions: settings.Partitions, ReplicationFactor: settings.ReplicationFactor, ConfigEntries: settings.Config}, false)
		if e != nil {
			var topicError *sarama.TopicError
			if !errors.As(e, &topicError) || topicError.Err != sarama.ErrTopicAlreadyExists {
				return e
			}
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		descriptions, e := admin.DescribeTopics(settings.Topics)
		if e != nil {
			return e
		}
		ready := len(descriptions) == len(settings.Topics)
		for _, topic := range descriptions {
			if topic.Err == sarama.ErrNoError {
				if len(topic.Partitions) != 6 {
					return fmt.Errorf("local topic partition mismatch: %s has %d partitions", topic.Name, len(topic.Partitions))
				}
			} else {
				ready = false
			}
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("local topic metadata deadline exceeded")
		case <-ticker.C:
		}
	}
	fmt.Println("Local fixture topics ready: six partitions, one broker; no cloud or HA claim")
	return nil
}
func migrate(ctx context.Context) error {
	for _, owner := range []string{"admission", "flash-sale", "order"} {
		prefix := strings.ToUpper(strings.ReplaceAll(owner, "-", "_"))
		p, e := postgres.Open(ctx, owner, os.Getenv(prefix+"_DATABASE_URL"))
		if e != nil {
			return e
		}
		e = eventstore.Migrate(ctx, p)
		if e == nil {
			switch owner {
			case "admission":
				e = admission.Migrate(ctx, p)
			case "flash-sale":
				e = flashsale.Migrate(ctx, p)
			}
		}
		p.Close()
		if e != nil {
			return e
		}
		fmt.Println(owner + ": owner schema current")
	}
	return nil
}
func matchesHealth(raw []byte, component, revision string) bool {
	var v struct {
		Component, Revision, Mode string
		BusinessReady             bool `json:"business_ready"`
		Capabilities              []string
	}
	if json.Unmarshal(raw, &v) != nil || v.Component != component || v.Revision != revision || v.Mode != "foundation" || v.BusinessReady {
		return false
	}
	if component == "flash-sale" || component == "admission" || component == "gateway" {
		return len(v.Capabilities) > 0
	}
	return true
}
func checkProcesses() error {
	raw, e := os.ReadFile(".cache/processes.json")
	if e != nil {
		return fmt.Errorf("local process record missing; run dev-up")
	}
	var processes []struct {
		Component, Binary string
		PID               int
	}
	if json.Unmarshal(raw, &processes) != nil || len(processes) != 6 {
		return fmt.Errorf("local process record invalid")
	}
	for _, p := range processes {
		cmd, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(p.PID), "cmdline"))
		if e != nil || string(bytes.Split(cmd, []byte{0})[0]) != p.Binary {
			return fmt.Errorf("%s is not running; inspect its log", p.Component)
		}
	}
	return nil
}
func wait(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := checkProcesses(); e != nil {
			return e
		}
		ready := true
		for key, component := range map[string]string{"ADMISSION_URL": "admission", "FLASH_SALE_URL": "flash-sale", "ORDER_URL": "order", "ANALYTICS_URL": "analytics", "GATEWAY_URL": "gateway", "CATALOG_URL": "fixture", "NOTIFICATION_URL": "fixture"} {
			base := os.Getenv(key)
			if base == "" {
				return fmt.Errorf("%s required", key)
			}
			r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/health/ready", nil)
			if e != nil {
				return fmt.Errorf("invalid %s", key)
			}
			response, e := client.Do(r)
			if e != nil {
				ready = false
				continue
			}
			var raw json.RawMessage
			decode := json.NewDecoder(response.Body).Decode(&raw)
			revision := os.Getenv("APP_REVISION")
			if revision == "" {
				revision = "development"
			}
			identity := matchesHealth(raw, component, revision)
			if component == "fixture" {
				var fixture struct{ Mode string }
				identity = decode == nil && json.Unmarshal(raw, &fixture) == nil && fixture.Mode == "fixture"
			}
			if response.StatusCode != 200 || decode != nil || !identity {
				ready = false
			}
			response.Body.Close()
		}
		if ready {
			fmt.Println("Local processes and fixture dependencies ready; sale browsing is available, peer acceptance remains pending")
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("foundation readiness deadline exceeded")
		case <-ticker.C:
		}
	}
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dev bootstrap|dev-up|dev-down|topics-local|migrate|seed|wait-ready")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	var e error
	switch os.Args[1] {
	case "bootstrap":
		e = run(ctx, "python3", "scripts/project.py", "bootstrap")
	case "topics-local":
		e = localTopics(ctx)
	case "migrate":
		e = migrate(ctx)
	case "seed":
		e = fixtures.Seed()
		if e == nil {
			fmt.Println("Seeded one operator, 100 shoppers and local service certificates; no business sale seeded")
		}
	case "dev-up":
		if os.Getenv("DEV_PEER_MODE") != "fixture" {
			e = fmt.Errorf("DEV_PEER_MODE=fixture required; real adapters remain external and need E4 approval")
			break
		}
		if e = run(ctx, "python3", "scripts/dev.py", "up"); e != nil {
			break
		}
		if e = localTopics(ctx); e != nil {
			break
		}
		if e = migrate(ctx); e != nil {
			break
		}
		if e = fixtures.Seed(); e != nil {
			break
		}
		if e = run(ctx, "python3", "scripts/dev.py", "launch"); e == nil {
			e = wait(ctx)
		}
	case "dev-down":
		if e = run(ctx, "python3", "scripts/dev.py", "stop"); e == nil {
			e = run(ctx, "python3", "scripts/dev.py", "down")
		}
	case "wait-ready":
		e = wait(ctx)
	default:
		e = fmt.Errorf("unknown foundation command")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
