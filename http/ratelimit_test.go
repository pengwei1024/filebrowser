package fbhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asdine/storm/v3"

	fbAuth "github.com/filebrowser/filebrowser/v2/auth"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

func TestRateLimiterPerUser(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(2, 100)

	if !l.allow("alice", "10.0.0.1") {
		t.Fatal("first attempt should be allowed")
	}
	l.recordFailure("alice", "10.0.0.1")
	l.recordFailure("alice", "10.0.0.2") // different IP, same user
	if l.allow("alice", "10.0.0.3") {
		t.Fatal("alice should be blocked after reaching the per-user limit, regardless of IP")
	}
	if !l.allow("bob", "10.0.0.3") {
		t.Fatal("bob should be unaffected by alice's failures")
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(100, 2)

	// Two failures from one IP for distinct usernames exhaust the IP budget
	// without any single username reaching its (much higher) limit.
	l.recordFailure("alice", "10.0.0.1")
	l.recordFailure("bob", "10.0.0.1")
	if l.allow("carol", "10.0.0.1") {
		t.Fatal("the shared IP should be blocked after reaching the per-IP limit")
	}
	if !l.allow("carol", "10.0.0.2") {
		t.Fatal("a different IP should be unaffected")
	}
}

func TestRateLimiterSuccessResets(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(2, 100)

	l.recordFailure("alice", "10.0.0.1")
	l.recordSuccess("alice", "10.0.0.1")
	// After a success the counter is cleared, so the next failure starts fresh
	// and does not trip the limit.
	l.recordFailure("alice", "10.0.0.1")
	if !l.allow("alice", "10.0.0.1") {
		t.Fatal("a single failure after a reset must not block")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 100)

	l.recordFailure("alice", "10.0.0.1")
	if l.allow("alice", "10.0.0.1") {
		t.Fatal("alice should be blocked within the window")
	}

	// Simulate the window elapsing by backdating the recorded failure.
	l.counters[userKey("alice")].windowStart = time.Now().Add(-2 * loginRateLimitWindow)
	if !l.allow("alice", "10.0.0.1") {
		t.Fatal("alice should be allowed again once the window has elapsed")
	}
}

func TestRateLimiterCaseInsensitiveUsername(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(1, 100)

	l.recordFailure("Admin", "10.0.0.1")
	if l.allow("admin", "10.0.0.2") {
		t.Fatal("username case variations must share one counter to prevent bypass")
	}
}

func TestAlertConfigured(t *testing.T) {
	t.Parallel()
	if alertConfigured("", 0) {
		t.Fatal("empty token and group must be treated as unconfigured")
	}
	if alertConfigured("tok", 0) {
		t.Fatal("a missing group must be treated as unconfigured")
	}
	if !alertConfigured("tok", 123) {
		t.Fatal("token and group together must be configured")
	}
	// notify on an unconfigured alert must be a harmless no-op.
	newAlerter().notify("", 0, "should not send")
}

// TestLoginAttackAlert verifies that crossing the per-user limit through the
// login handler fires exactly one webhook describing the attack (IP, username),
// using the destination configured in the web admin settings, and that further
// throttled requests do not spam more alerts.
func TestLoginAttackAlert(t *testing.T) {
	type received struct {
		queryToken string
		payload    map[string]interface{}
	}
	got := make(chan received, 8)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		_ = json.Unmarshal(body, &p)
		got <- received{queryToken: r.URL.Query().Get("access_token"), payload: p}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}

	hash, err := users.HashPwd("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	if err := st.Users.Save(&users.User{Username: "victim", Password: hash}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	if err := st.Settings.Save(&settings.Settings{
		Key:             []byte("test-signing-key"),
		AuthMethod:      fbAuth.MethodJSONAuth,
		LoginAlertToken: "secret-token",
		LoginAlertGroup: 1637558,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	if err := st.Auth.Save(&fbAuth.JSONAuth{}); err != nil {
		t.Fatalf("failed to save auther: %v", err)
	}

	const maxPerUser = 3
	limiter := newRateLimiter(maxPerUser, 100)
	alerter := &alerter{endpoint: stub.URL, client: stub.Client()}
	handler := handle(loginHandler(time.Hour, limiter, alerter), "", st, &settings.Server{})

	login := func() int {
		body := `{"username":"victim","password":"wrong"}`
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.RemoteAddr = "9.9.9.9:40000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Cross the per-user limit from one IP.
	for i := 0; i < maxPerUser; i++ {
		if code := login(); code != http.StatusForbidden {
			t.Fatalf("wrong-password attempt %d: got %d, want 403", i+1, code)
		}
	}

	var msg received
	select {
	case msg = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("expected an alert webhook call, got none")
	}

	if msg.queryToken != "secret-token" {
		t.Errorf("query access_token = %q, want secret-token", msg.queryToken)
	}
	if to, _ := msg.payload["to"].(float64); int(to) != 1637558 {
		t.Errorf("payload to = %v, want 1637558", msg.payload["to"])
	}
	if mt, _ := msg.payload["msg_type"].(string); mt != "text" {
		t.Errorf("payload msg_type = %q, want text", mt)
	}
	content, _ := msg.payload["content"].(string)
	if !strings.Contains(content, "9.9.9.9") || !strings.Contains(content, "victim") {
		t.Errorf("alert content should mention the IP and username, got %q", content)
	}

	// A throttled attempt (429, handled before recordFailure) must not alert.
	if code := login(); code != http.StatusTooManyRequests {
		t.Fatalf("victim should be locked out at this point, got %d", code)
	}
	select {
	case extra := <-got:
		t.Fatalf("unexpected second alert: %+v", extra.payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestLoginRateLimited exercises the throttle end to end through loginHandler:
// repeated wrong passwords for one user are eventually rejected with 429 — and
// crucially the correct password is rejected too once the user is locked — while
// an unrelated user from another IP keeps working.
func TestLoginRateLimited(t *testing.T) {
	const password = "correct-horse-battery-staple"

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}

	hash, err := users.HashPwd(password)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	for _, name := range []string{"victim", "other"} {
		if err := st.Users.Save(&users.User{Username: name, Password: hash}); err != nil {
			t.Fatalf("failed to save user %s: %v", name, err)
		}
	}
	if err := st.Settings.Save(&settings.Settings{
		Key:        []byte("test-signing-key"),
		AuthMethod: fbAuth.MethodJSONAuth,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	if err := st.Auth.Save(&fbAuth.JSONAuth{}); err != nil {
		t.Fatalf("failed to save auther: %v", err)
	}

	const maxPerUser = 3
	limiter := newRateLimiter(maxPerUser, 100)
	handler := handle(loginHandler(time.Hour, limiter, newAlerter()), "", st, &settings.Server{})

	login := func(username, pwd, ip string) int {
		body := `{"username":"` + username + `","password":"` + pwd + `"}`
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.RemoteAddr = ip + ":40000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Exhaust the per-user budget with wrong passwords.
	for i := 0; i < maxPerUser; i++ {
		if code := login("victim", "wrong", "1.2.3.4"); code != http.StatusForbidden {
			t.Fatalf("wrong-password attempt %d: got %d, want 403", i+1, code)
		}
	}

	// The next attempt is throttled — even though the password is correct.
	if code := login("victim", password, "1.2.3.4"); code != http.StatusTooManyRequests {
		t.Fatalf("VULNERABLE: locked user still processed; got %d, want 429", code)
	}

	// A different user from a different IP is unaffected and logs in fine.
	if code := login("other", password, "5.6.7.8"); code != http.StatusOK {
		t.Fatalf("unrelated user login got %d, want 200", code)
	}
}

// TestLoginSuccessClearsLimiter confirms a correct login resets the counter so
// earlier failures do not accumulate toward a later lockout.
func TestLoginSuccessClearsLimiter(t *testing.T) {
	const password = "correct-horse-battery-staple"

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}

	hash, err := users.HashPwd(password)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	if err := st.Users.Save(&users.User{Username: "victim", Password: hash}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	if err := st.Settings.Save(&settings.Settings{
		Key:        []byte("test-signing-key"),
		AuthMethod: fbAuth.MethodJSONAuth,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	if err := st.Auth.Save(&fbAuth.JSONAuth{}); err != nil {
		t.Fatalf("failed to save auther: %v", err)
	}

	const maxPerUser = 3
	limiter := newRateLimiter(maxPerUser, 100)
	handler := handle(loginHandler(time.Hour, limiter, newAlerter()), "", st, &settings.Server{})

	login := func(pwd string) int {
		body := `{"username":"victim","password":"` + pwd + `"}`
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.RemoteAddr = "1.2.3.4:40000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Stay one below the limit, succeed (which clears the counter), then fail
	// again repeatedly. Without the reset these would cross the threshold.
	for i := 0; i < maxPerUser-1; i++ {
		if code := login("wrong"); code != http.StatusForbidden {
			t.Fatalf("pre-success failure %d: got %d, want 403", i+1, code)
		}
	}
	if code := login(password); code != http.StatusOK {
		t.Fatalf("correct login got %d, want 200", code)
	}
	for i := 0; i < maxPerUser-1; i++ {
		if code := login("wrong"); code != http.StatusForbidden {
			t.Fatalf("post-success failure %d should still be 403 (counter reset), got %d", i+1, code)
		}
	}
}
