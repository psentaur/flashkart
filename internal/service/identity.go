package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"flashkart/internal/identity"
	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/invitation"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

func runIdentity(ctx context.Context, cfg config, log *slog.Logger) error {
	a := cfg.auth
	team := &identity.Team{Auth: a, Log: log}
	if a.Mode == "clerk" {
		secret := os.Getenv("CLERK_SECRET_KEY")
		if secret == "" {
			return errors.New("identity-api requires CLERK_SECRET_KEY")
		}
		client := &clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secret, HTTPClient: &http.Client{Timeout: 10e9}}}
		team.Users, team.Invitations = user.NewClient(client), invitation.NewClient(client)
		team.FrontendURL = os.Getenv("FLASHKART_FRONTEND_URL")
		if team.FrontendURL == "" {
			team.FrontendURL = a.Origins[0]
		}
		allowed := false
		for _, origin := range a.Origins {
			allowed = allowed || origin == team.FrontendURL
		}
		if !allowed {
			return errors.New("FLASHKART_FRONTEND_URL must match an allowed origin")
		}
	}
	return serve(ctx, "identity-api", cfg, log, nil, nil, serviceApp{wrap: team.Handler})
}
