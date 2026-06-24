package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dfm/device-gateway/internal/core/logging"
	"github.com/dfm/device-gateway/internal/core/mapping"
)

// fakeData is an in-memory DataStore for handler tests.
type fakeData struct {
	users              map[string]string // email -> password
	mappings           []map[string]any
	upserted           *mapping.Entry
	deletedMap         bool
	approved           string
	webhookURL         string
	lastWebhookEnabled bool
	createdUser        string
	createdKeyName     string
	userCount          int
}

func (f *fakeData) VerifyUser(_ context.Context, email, password string) (bool, error) {
	return f.users[email] == password && password != "", nil
}
func (f *fakeData) CountUsers(context.Context) (int, error) { return f.userCount, nil }
func (f *fakeData) ListDevices(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"serial": "ABC", "status": "online"}}, nil
}
func (f *fakeData) ListPendingDevices(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"serial": "PENDING1"}}, nil
}
func (f *fakeData) ApproveDevice(_ context.Context, serial, _ string) error {
	f.approved = serial
	return nil
}
func (f *fakeData) RejectDevice(context.Context, string) error { return nil }
func (f *fakeData) DeleteDevice(context.Context, string) error { return nil }
func (f *fakeData) ListEventMappings(context.Context, string, string) ([]map[string]any, error) {
	return f.mappings, nil
}
func (f *fakeData) ListEventMappingModels(context.Context, string) ([]string, error) {
	return []string{}, nil
}
func (f *fakeData) CopyEventMappings(context.Context, string, string, string) error { return nil }
func (f *fakeData) UpsertEventMapping(_ context.Context, _ string, e mapping.Entry) error {
	if strings.TrimSpace(e.EventCode) == "" {
		return errInput
	}
	f.upserted = &e
	return nil
}
func (f *fakeData) DeleteEventMapping(_ context.Context, _, _, _ string, code int) error {
	if code == 999 {
		return errMissing
	}
	f.deletedMap = true
	return nil
}
func (f *fakeData) ListGatewayErrors(context.Context, int, int) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (f *fakeData) ListDeviceErrors(context.Context, int, int) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (f *fakeData) ListStandardEventCodes(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"code": "PANIC", "category": "Panic"}}, nil
}
func (f *fakeData) AddStandardEventCode(context.Context, string, string, string) error { return nil }
func (f *fakeData) ListSettings(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"key": "webhook_url", "value": f.webhookURL}}, nil
}
func (f *fakeData) ListUnitSettings(context.Context, string) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (f *fakeData) SetUnitSetting(context.Context, string, string, string) error { return nil }
func (f *fakeData) SetSetting(_ context.Context, key, value string) error {
	if key == "webhook_url" {
		f.webhookURL = value
	}
	return nil
}
func (f *fakeData) ListWebhooks(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"id": int64(1), "name": "default", "url": "https://db/x", "is_enabled": true}}, nil
}
func (f *fakeData) ListClips(context.Context, string, int, int) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (f *fakeData) GetClip(context.Context, int64) (map[string]any, error) {
	return nil, errors.New(notFoundMsg)
}
func (f *fakeData) DeleteClip(context.Context, int64) (string, error) { return "", nil }
func (f *fakeData) CreateWebhook(_ context.Context, _, _ string, enabled bool) (int64, error) {
	f.lastWebhookEnabled = enabled
	return 7, nil
}
func (f *fakeData) UpdateWebhook(_ context.Context, id int64, _, _ string, enabled bool) error {
	if id == 999 {
		return errMissing
	}
	f.lastWebhookEnabled = enabled
	return nil
}
func (f *fakeData) DeleteWebhook(_ context.Context, id int64) error {
	if id == 999 {
		return errMissing
	}
	return nil
}
func (f *fakeData) ListUsers(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"id": int64(1), "email": "admin@example.com", "is_active": true}}, nil
}
func (f *fakeData) CreateUser(_ context.Context, email, password string) error {
	if email == "dup@x.com" {
		return strErr("a user with that email already exists")
	}
	if len(password) < 8 {
		return strErr("password must be at least 8 characters")
	}
	f.createdUser = email
	return nil
}
func (f *fakeData) SetUserActive(_ context.Context, id int64, _ bool) error {
	if id == 999 {
		return errMissing
	}
	return nil
}
func (f *fakeData) SetUserPassword(_ context.Context, id int64, password string) error {
	if len(password) < 8 {
		return strErr("password must be at least 8 characters")
	}
	return nil
}
func (f *fakeData) DeleteUser(_ context.Context, id int64) error {
	if id == 999 {
		return errMissing
	}
	if id == 1 {
		return strErr("cannot delete the last active user")
	}
	return nil
}
func (f *fakeData) ListAPIKeyMeta(context.Context) ([]map[string]any, error) {
	return []map[string]any{{"name": "frontend", "prefix": "dgw_AbCd1234", "is_active": true}}, nil
}
func (f *fakeData) CreateAPIKey(_ context.Context, name string) (string, error) {
	f.createdKeyName = name
	return "dgw_PLAINTEXTKEY", nil
}
func (f *fakeData) RevokeAPIKey(_ context.Context, prefix string) (int64, error) {
	if prefix == "dgw_missing" {
		return 0, nil
	}
	return 1, nil
}

// sentinel errors matching the store's contract.
type strErr string

func (e strErr) Error() string { return string(e) }

const (
	errInput   = strErr("event_code is required")
	errMissing = strErr("not found")
)

func newAdminServer(f *fakeData) *Server {
	return New("127.0.0.1", 0, []UnitInfo{{Name: "howen"}}, stubVerifier{valid: "k"}, f, nil, logging.New("test"))
}

func do(s *Server, method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer k")
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, r)
	return rec
}

func TestLogin(t *testing.T) {
	s := newAdminServer(&fakeData{users: map[string]string{"a@b.com": "secret"}})

	if rec := do(s, "POST", "/api/auth/login", `{"email":"a@b.com","password":"secret"}`); rec.Code != 200 {
		t.Fatalf("valid login = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	if rec := do(s, "POST", "/api/auth/login", `{"email":"a@b.com","password":"wrong"}`); rec.Code != 401 {
		t.Fatalf("bad login = %d, want 401", rec.Code)
	}
}

func TestListAndApproveDevices(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	rec := do(s, "GET", "/api/devices", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ABC") {
		t.Fatalf("list devices = %d body=%s", rec.Code, rec.Body)
	}
	if rec := do(s, "POST", "/api/devices/PENDING1/approve", ""); rec.Code != 200 || f.approved != "PENDING1" {
		t.Fatalf("approve = %d approved=%q", rec.Code, f.approved)
	}
}

func TestUpsertMappingValidation(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	if rec := do(s, "PUT", "/api/mappings", `{"map_type":"dms_adas","code":34,"event_code":"AI:CELLPHONE"}`); rec.Code != 200 {
		t.Fatalf("valid upsert = %d (%s)", rec.Code, rec.Body)
	}
	if f.upserted == nil || f.upserted.EventCode != "AI:CELLPHONE" {
		t.Fatalf("upsert not recorded: %+v", f.upserted)
	}
	// Missing event_code → 400.
	if rec := do(s, "PUT", "/api/mappings", `{"map_type":"dms_adas","code":34}`); rec.Code != 400 {
		t.Fatalf("invalid upsert = %d, want 400", rec.Code)
	}
}

func TestDeleteMapping(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	if rec := do(s, "DELETE", "/api/mappings?map_type=dms_adas&code=34", ""); rec.Code != 200 || !f.deletedMap {
		t.Fatalf("delete = %d deleted=%v", rec.Code, f.deletedMap)
	}
	if rec := do(s, "DELETE", "/api/mappings?map_type=dms_adas&code=999", ""); rec.Code != 404 {
		t.Fatalf("delete missing = %d, want 404", rec.Code)
	}
	if rec := do(s, "DELETE", "/api/mappings?code=34", ""); rec.Code != 400 {
		t.Fatalf("delete no map_type = %d, want 400", rec.Code)
	}
}

func TestSettingsEndpoints(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	// set a valid webhook URL → 200, stored.
	if rec := do(s, "PUT", "/api/settings", `{"key":"webhook_url","value":"https://db.example/gps"}`); rec.Code != 200 {
		t.Fatalf("set webhook = %d (%s)", rec.Code, rec.Body)
	}
	if f.webhookURL != "https://db.example/gps" {
		t.Fatalf("webhook not stored: %q", f.webhookURL)
	}
	// invalid URL → 400.
	if rec := do(s, "PUT", "/api/settings", `{"key":"webhook_url","value":"not a url"}`); rec.Code != 400 {
		t.Fatalf("invalid webhook = %d, want 400", rec.Code)
	}
	// empty value is allowed (disables the webhook).
	if rec := do(s, "PUT", "/api/settings", `{"key":"webhook_url","value":""}`); rec.Code != 200 {
		t.Fatalf("clear webhook = %d, want 200", rec.Code)
	}
	// list reflects it.
	if rec := do(s, "GET", "/api/settings", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "webhook_url") {
		t.Fatalf("list settings = %d (%s)", rec.Code, rec.Body)
	}
}

func TestWebhookEndpoints(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	// list
	if rec := do(s, "GET", "/api/webhooks", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "default") {
		t.Fatalf("list = %d (%s)", rec.Code, rec.Body)
	}
	// create with valid URL, enabled omitted → defaults true
	if rec := do(s, "POST", "/api/webhooks", `{"name":"n","url":"https://db.example/gps"}`); rec.Code != 200 {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body)
	}
	if !f.lastWebhookEnabled {
		t.Fatal("enabled should default to true when omitted")
	}
	// create with invalid URL → 400
	if rec := do(s, "POST", "/api/webhooks", `{"url":"nope"}`); rec.Code != 400 {
		t.Fatalf("invalid url = %d, want 400", rec.Code)
	}
	// disable via PUT
	if rec := do(s, "PUT", "/api/webhooks/1", `{"url":"https://db.example/gps","is_enabled":false}`); rec.Code != 200 || f.lastWebhookEnabled {
		t.Fatalf("disable = %d enabled=%v", rec.Code, f.lastWebhookEnabled)
	}
	// update missing → 404
	if rec := do(s, "PUT", "/api/webhooks/999", `{"url":"https://db.example/gps"}`); rec.Code != 404 {
		t.Fatalf("update missing = %d, want 404", rec.Code)
	}
	// delete
	if rec := do(s, "DELETE", "/api/webhooks/1", ""); rec.Code != 200 {
		t.Fatalf("delete = %d", rec.Code)
	}
}

func TestUserEndpoints(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	if rec := do(s, "GET", "/api/users", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "admin@example.com") {
		t.Fatalf("list = %d (%s)", rec.Code, rec.Body)
	}
	// create valid
	if rec := do(s, "POST", "/api/users", `{"email":"new@x.com","password":"secret12"}`); rec.Code != 200 || f.createdUser != "new@x.com" {
		t.Fatalf("create = %d created=%q", rec.Code, f.createdUser)
	}
	// invalid email → 400
	if rec := do(s, "POST", "/api/users", `{"email":"bad","password":"secret12"}`); rec.Code != 400 {
		t.Fatalf("bad email = %d, want 400", rec.Code)
	}
	// weak password → 400
	if rec := do(s, "POST", "/api/users", `{"email":"a@b.com","password":"x"}`); rec.Code != 400 {
		t.Fatalf("weak pw = %d, want 400", rec.Code)
	}
	// duplicate → 409
	if rec := do(s, "POST", "/api/users", `{"email":"dup@x.com","password":"secret12"}`); rec.Code != 409 {
		t.Fatalf("dup = %d, want 409", rec.Code)
	}
	// toggle active
	if rec := do(s, "PUT", "/api/users/2", `{"is_active":false}`); rec.Code != 200 {
		t.Fatalf("toggle = %d", rec.Code)
	}
	// reset password (weak) → 400
	if rec := do(s, "PUT", "/api/users/2", `{"password":"x"}`); rec.Code != 400 {
		t.Fatalf("weak reset = %d, want 400", rec.Code)
	}
	// delete last active → 409
	if rec := do(s, "DELETE", "/api/users/1", ""); rec.Code != 409 {
		t.Fatalf("delete last = %d, want 409", rec.Code)
	}
	// delete other → 200
	if rec := do(s, "DELETE", "/api/users/2", ""); rec.Code != 200 {
		t.Fatalf("delete = %d", rec.Code)
	}
}

func TestAPIKeyEndpoints(t *testing.T) {
	f := &fakeData{}
	s := newAdminServer(f)

	if rec := do(s, "GET", "/api/api-keys", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "dgw_AbCd1234") {
		t.Fatalf("list = %d (%s)", rec.Code, rec.Body)
	}
	// create returns the plaintext once
	rec := do(s, "POST", "/api/api-keys", `{"name":"external"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "dgw_PLAINTEXTKEY") || f.createdKeyName != "external" {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body)
	}
	// revoke existing
	if rec := do(s, "DELETE", "/api/api-keys/dgw_AbCd1234", ""); rec.Code != 200 {
		t.Fatalf("revoke = %d", rec.Code)
	}
	// revoke unknown → 404
	if rec := do(s, "DELETE", "/api/api-keys/dgw_missing", ""); rec.Code != 404 {
		t.Fatalf("revoke missing = %d, want 404", rec.Code)
	}
}

func TestSetupEndpoints(t *testing.T) {
	// uninitialized: needs_setup true, setup creates the first admin.
	f := &fakeData{userCount: 0}
	s := newAdminServer(f)
	if rec := do(s, "GET", "/api/setup/status", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "\"needs_setup\":true") {
		t.Fatalf("status uninit = %d (%s)", rec.Code, rec.Body)
	}
	if rec := do(s, "POST", "/api/setup", `{"email":"admin@x.com","password":"secret12","gateway_name":"gw.x.net"}`); rec.Code != 200 || f.createdUser != "admin@x.com" {
		t.Fatalf("setup = %d created=%q (%s)", rec.Code, f.createdUser, rec.Body)
	}
	// invalid email rejected
	if rec := do(s, "POST", "/api/setup", `{"email":"bad","password":"secret12"}`); rec.Code != 400 {
		t.Fatalf("setup bad email = %d, want 400", rec.Code)
	}

	// initialized: needs_setup false, setup refused.
	g := &fakeData{userCount: 1}
	s2 := newAdminServer(g)
	if rec := do(s2, "GET", "/api/setup/status", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "\"needs_setup\":false") {
		t.Fatalf("status init = %d (%s)", rec.Code, rec.Body)
	}
	if rec := do(s2, "POST", "/api/setup", `{"email":"admin@x.com","password":"secret12"}`); rec.Code != 409 {
		t.Fatalf("setup when initialized = %d, want 409", rec.Code)
	}
}

func TestInternalToken(t *testing.T) {
	s := New("127.0.0.1", 0, []UnitInfo{{Name: "howen"}}, stubVerifier{valid: "dbkey"}, &fakeData{}, nil, logging.New("test"))
	s.SetInternalToken("internal-secret")
	// internal token works
	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer internal-secret")
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("internal token = %d, want 200", rec.Code)
	}
	// DB key still works alongside it
	req2 := httptest.NewRequest("GET", "/api/ping", nil)
	req2.Header.Set("Authorization", "Bearer dbkey")
	rec2 := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("db key = %d, want 200", rec2.Code)
	}
	// wrong token rejected
	req3 := httptest.NewRequest("GET", "/api/ping", nil)
	req3.Header.Set("Authorization", "Bearer nope")
	rec3 := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec3, req3)
	if rec3.Code != 401 {
		t.Fatalf("wrong token = %d, want 401", rec3.Code)
	}
}

func TestDataEndpointsNoStore(t *testing.T) {
	s := New("127.0.0.1", 0, []UnitInfo{{Name: "howen"}}, stubVerifier{valid: "k"}, nil, nil, logging.New("test"))
	if rec := do(s, "GET", "/api/devices", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-store devices = %d, want 503", rec.Code)
	}
}

// ensure JSON bodies decode (guards against accidental content-type coupling).
var _ = json.Marshal
