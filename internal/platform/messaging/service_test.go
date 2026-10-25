package messaging

import (
	"context"
	"errors"
	"flash-sale/internal/platform/events"
	"fmt"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDependencyOutageClassification(t *testing.T) {
	network := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	invalid := &events.QuarantineError{Reason: "invalid payload"}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"handler error", errors.New("handler failed"), false},
		{"constraint error", &pgconn.PgError{Code: "23505"}, false},
		{"database starting", &pgconn.PgError{Code: "57P03"}, true},
		{"wrapped database starting", fmt.Errorf("database: %w", &pgconn.PgError{Code: "57P03"}), true},
		{"connection capacity", &pgconn.PgError{Code: "53300"}, true},
		{"wrapped connection capacity", fmt.Errorf("database: %w", &pgconn.PgError{Code: "53300"}), true},
		{"quarantine wraps database outage", errors.Join(invalid, &pgconn.PgError{Code: "57P03"}), false},
		{"quarantine", invalid, false},
		{"quarantine wraps network", errors.Join(invalid, network), false},
		{"network", network, true},
		{"wrapped network", fmt.Errorf("dependency: %w", network), true},
		{"handler budget", context.DeadlineExceeded, true},
		{"wrapped handler budget", fmt.Errorf("handler: %w", context.DeadlineExceeded), true},
		{"cancellation", context.Canceled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dependencyOutage(tc.err); got != tc.want {
				t.Fatalf("outage=%v, want %v", got, tc.want)
			}
		})
	}
}
