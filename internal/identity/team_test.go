package identity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/invitation"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

type fakeUsers struct {
	value    *clerk.User
	existing bool
	updates  int
}

func (f *fakeUsers) List(context.Context, *user.ListParams) (*clerk.UserList, error) {
	list := &clerk.UserList{}
	if f.existing {
		list.Users = []*clerk.User{f.value}
		list.TotalCount = 1
	}
	return list, nil
}
func (f *fakeUsers) Get(context.Context, string) (*clerk.User, error) { return f.value, nil }
func (f *fakeUsers) UpdateMetadata(_ context.Context, _ string, p *user.UpdateMetadataParams) (*clerk.User, error) {
	f.updates++
	f.value.PublicMetadata = *p.PublicMetadata
	return f.value, nil
}

type fakeInvites struct {
	creates, revokes int
	pending          bool
}

func (f *fakeInvites) List(context.Context, *invitation.ListParams) (*clerk.InvitationList, error) {
	list := &clerk.InvitationList{}
	if f.pending {
		list.Invitations = []*clerk.Invitation{{ID: "inv_test", EmailAddress: "new@example.com", Status: "pending"}}
		list.TotalCount = 1
	}
	return list, nil
}
func (f *fakeInvites) Create(_ context.Context, p *invitation.CreateParams) (*clerk.Invitation, error) {
	f.creates++
	if *p.ExpiresInDays != 7 || *p.IgnoreExisting || !*p.Notify || string(*p.PublicMetadata) != `{"role":"admin"}` {
		panic("invalid invitation policy")
	}
	f.pending = true
	return &clerk.Invitation{ID: "inv_test", EmailAddress: p.EmailAddress, Status: "pending"}, nil
}
func (f *fakeInvites) Revoke(context.Context, string) (*clerk.Invitation, error) {
	f.revokes++
	f.pending = false
	return &clerk.Invitation{ID: "inv_test", Status: "revoked"}, nil
}

func TestTeamAccessAndInvitations(t *testing.T) {
	u := &fakeUsers{value: &clerk.User{ID: "user_target", PrimaryEmailAddressID: clerk.String("email_1"), EmailAddresses: []*clerk.EmailAddress{{ID: "email_1", EmailAddress: "existing@example.com", Verification: &clerk.Verification{Status: "verified"}}}}}
	i := &fakeInvites{}
	team := &Team{Auth: &Auth{OwnerID: "user_owner"}, Users: u, Invitations: i, FrontendURL: "http://localhost:5173", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := team.Handler(http.NotFoundHandler())
	request := func(method, path, body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if role != "" {
			id := "user_" + role
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, Principal{id, role}))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, role := range []string{"", "customer", "admin"} {
		w := request("PATCH", "/api/admin/team/users/user_target", `{"role":"admin"}`, role)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("role=%q status=%d", role, w.Code)
		}
	}
	if u.updates != 0 {
		t.Fatal("unauthorized role mutation")
	}
	if w := request("PATCH", "/api/admin/team/users/user_owner", `{"role":"customer"}`, "owner"); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	if w := request("PATCH", "/api/admin/team/users/user_target", `{"role":"owner"}`, "owner"); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	for _, role := range []string{"admin", "customer"} {
		w := request("PATCH", "/api/admin/team/users/user_target", `{"role":"`+role+`"}`, "owner")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result userView
		json.Unmarshal(w.Body.Bytes(), &result)
		if result.Role != role {
			t.Fatal(result)
		}
	}
	u.value.EmailAddresses[0].Verification.Status = "unverified"
	if w := request("PATCH", "/api/admin/team/users/user_target", `{"role":"admin"}`, "owner"); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	for n := 0; n < 2; n++ {
		w := request("POST", "/api/admin/team/invitations", `{"email":"new@example.com"}`, "owner")
		if w.Code != 200 && w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	if i.creates != 1 {
		t.Fatal("duplicate invitation sent")
	}
	if w := request("DELETE", "/api/admin/team/invitations/inv_test", "", "owner"); w.Code != 200 || i.revokes != 1 {
		t.Fatal(w.Body.String())
	}
	u.existing = true
	if w := request("POST", "/api/admin/team/invitations", `{"email":"existing@example.com"}`, "owner"); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	for _, email := range []string{"invalid", "Name <x@example.com>", "a@example.com\nBcc:x@example.com"} {
		body, _ := json.Marshal(map[string]string{"email": email})
		if w := request("POST", "/api/admin/team/invitations", string(body), "owner"); w.Code != 400 {
			t.Fatal(w.Body.String())
		}
	}
	if w := request("GET", "/api/admin/team/users?limit=-1", "", "owner"); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
}
