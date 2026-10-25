package fbhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// defaultAlertEndpoint is the Baidu IM group-message webhook. It is a field on
// alerter (not hardcoded in send) so tests can point it at a stub server.
const defaultAlertEndpoint = "http://qy.im.baidu.com/msgt/api/sendMsgToGroup"

// alerter posts a human-readable notice to a Baidu IM group when the login
// throttle flags a suspected attack. The destination (access token and group)
// is supplied per call because it is edited at runtime through the web admin
// Settings page rather than fixed at startup; only the reusable HTTP client and
// endpoint live on the struct.
type alerter struct {
	endpoint string
	client   *http.Client
}

func newAlerter() *alerter {
	return &alerter{
		endpoint: defaultAlertEndpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// alertConfigured reports whether a usable alert destination has been set. When
// it returns false the feature is simply off — nothing is sent.
func alertConfigured(token string, group int) bool {
	return token != "" && group != 0
}

// notify sends content asynchronously so the login request is never blocked or
// slowed by the outbound webhook call. It is a no-op when not configured.
func (a *alerter) notify(token string, group int, content string) {
	if a == nil || !alertConfigured(token, group) {
		return
	}
	go func() {
		if err := a.send(token, group, content); err != nil {
			log.Printf("[WARN] login alert: %v", err)
		}
	}()
}

// send delivers content synchronously and returns any error, so the admin
// "test" button can report whether the configured destination actually works.
func (a *alerter) send(token string, group int, content string) error {
	// The working request carries the token both in the query string and in the
	// body; mirror that.
	payload := map[string]interface{}{
		"to":           group,
		"msg_type":     "text",
		"content":      content,
		"access_token": token,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode payload: %w", err)
	}

	endpoint := a.endpoint + "?access_token=" + url.QueryEscape(token)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
