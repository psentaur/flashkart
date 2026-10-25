package main

import (
	"context"
	"encoding/json"
	"flash-sale/tests/scenarios"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--list" {
		names := []string{}
		for _, s := range scenarios.List() {
			names = append(names, s.Name)
		}
		_ = json.NewEncoder(os.Stdout).Encode(names)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if e := scenarios.Run(ctx, os.Args[1]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
