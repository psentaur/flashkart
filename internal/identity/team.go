package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/invitation"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

type Users interface {
	List(context.Context, *user.ListParams) (*clerk.UserList, error)
	Get(context.Context, string) (*clerk.User, error)
	UpdateMetadata(context.Context, string, *user.UpdateMetadataParams) (*clerk.User, error)
}
type Invitations interface {
	List(context.Context, *invitation.ListParams) (*clerk.InvitationList, error)
	Create(context.Context, *invitation.CreateParams) (*clerk.Invitation, error)
	Revoke(context.Context, string) (*clerk.Invitation, error)
}
type Team struct {
	Auth        *Auth
	Users       Users
	Invitations Invitations
	FrontendURL string
	Log         *slog.Logger
}
type userView struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Verified bool   `json:"verified"`
	Disabled bool   `json:"disabled"`
}
type inviteView struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	ExpiresAt *int64 `json:"expires_at"`
}

func primaryEmail(u *clerk.User) (string, bool) {
	for _, email := range u.EmailAddresses {
		if email != nil && u.PrimaryEmailAddressID != nil && email.ID == *u.PrimaryEmailAddressID {
			return email.EmailAddress, email.Verification != nil && email.Verification.Status == "verified"
		}
	}
	return "", false
}
func (t *Team) view(u *clerk.User) userView {
	var metadata struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(u.PublicMetadata, &metadata)
	email, verified := primaryEmail(u)
	return userView{u.ID, email, t.Auth.Role(u.ID, metadata.Role), verified, u.Banned || u.Locked}
}
func invitationView(i *clerk.Invitation) inviteView {
	return inviteView{i.ID, i.EmailAddress, i.Status, i.ExpiresAt}
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		Error(w, 400, "INVALID_ARGUMENT", "Invalid request.")
		return false
	}
	return true
}
func (t *Team) audit(r *http.Request, action, target, outcome string) {
	p, _ := FromContext(r.Context())
	t.Log.Info("team access change", "actor", p.UserID, "action", action, "target", target, "outcome", outcome)
}
func (t *Team) failure(w http.ResponseWriter, r *http.Request, action, target string, err error) {
	t.audit(r, action, target, "failed")
	var apiError *clerk.APIErrorResponse
	if errors.As(err, &apiError) {
		switch apiError.HTTPStatusCode {
		case 404:
			Error(w, 404, "NOT_FOUND", "The user or invitation no longer exists.")
			return
		case 400, 409, 422:
			Error(w, 409, "CONFLICT", "The account or invitation has changed. Refresh and try again.")
			return
		case 429:
			w.Header().Set("Retry-After", "60")
			Error(w, 429, "RATE_LIMITED", "Please wait before trying again.")
			return
		}
	}
	Error(w, 503, "UNAVAILABLE", "Clerk could not confirm this operation. Refresh before retrying.")
}
func page(r *http.Request) (clerk.ListParams, bool) {
	limit, offset := int64(20), int64(0)
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return clerk.ListParams{}, false
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return clerk.ListParams{}, false
		}
	}
	return clerk.ListParams{Limit: &limit, Offset: &offset}, limit >= 1 && limit <= 100 && offset >= 0 && offset <= 100000
}

func (t *Team) Handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok {
			Error(w, 401, "UNAUTHENTICATED", "Sign in to continue.")
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("GET /api/admin/team/users", t.owner(t.listUsers))
	mux.HandleFunc("PATCH /api/admin/team/users/{id}", t.owner(t.updateRole))
	mux.HandleFunc("GET /api/admin/team/invitations", t.owner(t.listInvites))
	mux.HandleFunc("POST /api/admin/team/invitations", t.owner(t.invite))
	mux.HandleFunc("DELETE /api/admin/team/invitations/{id}", t.owner(t.revoke))
	mux.Handle("/", fallback)
	return mux
}
func (t *Team) owner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok {
			Error(w, 401, "UNAUTHENTICATED", "Sign in to continue.")
			return
		}
		if p.Role != "owner" || p.UserID != t.Auth.OwnerID {
			Error(w, 403, "FORBIDDEN", "Only the owner can manage the team.")
			return
		}
		if t.Users == nil || t.Invitations == nil {
			Error(w, 503, "UNAVAILABLE", "Team management requires Clerk.")
			return
		}
		next(w, r)
	}
}
func (t *Team) listUsers(w http.ResponseWriter, r *http.Request) {
	paging, ok := page(r)
	if !ok {
		Error(w, 400, "INVALID_ARGUMENT", "Invalid pagination.")
		return
	}
	p := &user.ListParams{ListParams: paging}
	if email := strings.TrimSpace(r.URL.Query().Get("email")); email != "" {
		p.EmailAddressQuery = &email
	}
	list, err := t.Users.List(r.Context(), p)
	if err != nil {
		t.failure(w, r, "list_users", "", err)
		return
	}
	views := make([]userView, 0, len(list.Users))
	for _, u := range list.Users {
		views = append(views, t.view(u))
	}
	respond(w, 200, map[string]any{"data": views, "total_count": list.TotalCount})
}
func (t *Team) updateRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == t.Auth.OwnerID {
		Error(w, 409, "OWNER_PROTECTED", "The owner's access cannot be changed here.")
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Role != "admin" && input.Role != "customer" {
		Error(w, 400, "INVALID_ARGUMENT", "Choose admin or customer.")
		return
	}
	u, err := t.Users.Get(r.Context(), id)
	if err != nil {
		t.failure(w, r, "role_change", id, err)
		return
	}
	view := t.view(u)
	if input.Role == "admin" && (!view.Verified || view.Disabled) {
		Error(w, 409, "UNVERIFIED_USER", "An admin must have a verified primary email and an enabled account.")
		return
	}
	metadata, _ := json.Marshal(map[string]string{"role": input.Role})
	u, err = t.Users.UpdateMetadata(r.Context(), id, &user.UpdateMetadataParams{PublicMetadata: (*json.RawMessage)(&metadata)})
	if err != nil {
		t.failure(w, r, "role_change", id, err)
		return
	}
	t.audit(r, "role_"+input.Role, id, "succeeded")
	respond(w, 200, t.view(u))
}
func (t *Team) listInvites(w http.ResponseWriter, r *http.Request) {
	paging, ok := page(r)
	if !ok {
		Error(w, 400, "INVALID_ARGUMENT", "Invalid pagination.")
		return
	}
	list, err := t.Invitations.List(r.Context(), &invitation.ListParams{ListParams: paging})
	if err != nil {
		t.failure(w, r, "list_invitations", "", err)
		return
	}
	views := make([]inviteView, 0, len(list.Invitations))
	for _, i := range list.Invitations {
		views = append(views, invitationView(i))
	}
	respond(w, 200, map[string]any{"data": views, "total_count": list.TotalCount})
}
func (t *Team) invite(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &input) {
		return
	}
	email := strings.TrimSpace(input.Email)
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 {
		Error(w, 400, "INVALID_ARGUMENT", "Enter a valid email address.")
		return
	}
	users, err := t.Users.List(r.Context(), &user.ListParams{EmailAddresses: []string{email}})
	if err != nil {
		t.failure(w, r, "invite", email, err)
		return
	}
	if len(users.Users) > 0 {
		Error(w, 409, "USER_EXISTS", "This person already has an account. Find them in Users and grant admin access.")
		return
	}
	pending, err := t.Invitations.List(r.Context(), &invitation.ListParams{Query: &email, Statuses: []string{"pending"}})
	if err != nil {
		t.failure(w, r, "invite", email, err)
		return
	}
	for _, i := range pending.Invitations {
		if strings.EqualFold(i.EmailAddress, email) {
			respond(w, 200, invitationView(i))
			return
		}
	}
	metadata := json.RawMessage(`{"role":"admin"}`)
	i, err := t.Invitations.Create(r.Context(), &invitation.CreateParams{EmailAddress: email, PublicMetadata: &metadata,
		RedirectURL: clerk.String(t.FrontendURL + "/sign-up"), Notify: clerk.Bool(true), ExpiresInDays: clerk.Int64(7), IgnoreExisting: clerk.Bool(false)})
	if err != nil {
		t.failure(w, r, "invite", email, err)
		return
	}
	t.audit(r, "invite", email, "succeeded")
	respond(w, 201, invitationView(i))
}
func (t *Team) revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	i, err := t.Invitations.Revoke(r.Context(), id)
	if err != nil {
		t.failure(w, r, "revoke_invitation", id, err)
		return
	}
	t.audit(r, "revoke_invitation", id, "succeeded")
	respond(w, 200, invitationView(i))
}
