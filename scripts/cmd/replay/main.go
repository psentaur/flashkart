package main

import (
	"context"
	"flag"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/postgres"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	owner := flag.String("owner", "", "admission, flash-sale or order")
	id := flag.Int64("id", 0, "inspectable failed-work ID")
	reason := flag.String("reason", "", "audit reason (required)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := func() error {
		v, e := auth.Load(os.Getenv("JWT_PUBLIC_KEY_FILE"), env("JWT_ISSUER", "flashkart-local"), env("JWT_AUDIENCE", "flashkart"))
		if e != nil {
			return e
		}
		p, e := v.Verify(os.Getenv("REPLAY_OPERATOR_TOKEN"))
		if e != nil {
			return e
		}
		prefix := strings.ToUpper(strings.ReplaceAll(*owner, "-", "_"))
		pool, e := postgres.Open(ctx, *owner, os.Getenv(prefix+"_DATABASE_URL"))
		if e != nil {
			return e
		}
		defer pool.Close()
		return (messaging.RetryStore{Pool: pool}).Replay(ctx, *id, p, *reason)
	}()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("Replay scheduled with original identity and an operator audit record")
}
func env(k, f string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return f
}
