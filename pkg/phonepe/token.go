package phonepe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tokenRefreshSkew refreshes a token this long before it expires.
const tokenRefreshSkew = 60 * time.Second

// tokenSource fetches and caches the OAuth access token PhonePe wants as "Authorization: O-Bearer <token>".
// Tokens last about a week, so one source is shared by every client built for the same credentials.
type tokenSource struct {
	creds Credentials
	http  httpDoer
	now   func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newTokenSource(creds Credentials, doer httpDoer) *tokenSource {
	return &tokenSource{creds: creds, http: doer, now: time.Now}
}

// Token returns a valid access token, refreshing it when it is about to expire.
func (t *tokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && t.now().Before(t.expiresAt.Add(-tokenRefreshSkew)) {
		return t.token, nil
	}
	return t.refreshLocked(ctx)
}

// Invalidate drops the cached token so the next call fetches a new one (used after a 401).
func (t *tokenSource) Invalidate(stale string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token == stale {
		t.token = ""
		t.expiresAt = time.Time{}
	}
}

func (t *tokenSource) refreshLocked(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("client_id", t.creds.ClientID)
	form.Set("client_secret", t.creds.ClientSecret)
	form.Set("client_version", strconv.Itoa(t.creds.ClientVersion))
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.creds.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", newAPIError(resp.StatusCode, resp.Header, raw)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"` // epoch seconds
		IssuedAt    int64  `json:"issued_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("phonepe: token response had no access_token")
	}

	t.token = out.AccessToken
	if out.ExpiresAt > 0 {
		t.expiresAt = time.Unix(out.ExpiresAt, 0)
	} else {
		// No expiry given: assume a short life so we re-fetch soon rather than use a dead token.
		t.expiresAt = t.now().Add(time.Hour)
	}
	return t.token, nil
}

// sharedTokenSources keeps one tokenSource per credential set. The service rebuilds API clients every few
// minutes, and fetching a token each time would be wasteful and could trip PhonePe's auth rate limits.
var sharedTokenSources sync.Map // key string -> *tokenSource

func sharedTokenSource(creds Credentials, doer httpDoer) *tokenSource {
	sum := sha256.Sum256([]byte(creds.ClientSecret))
	key := fmt.Sprintf("%s|%d|%t|%s", creds.ClientID, creds.ClientVersion, creds.Sandbox, hex.EncodeToString(sum[:8]))
	if v, ok := sharedTokenSources.Load(key); ok {
		return v.(*tokenSource)
	}
	v, _ := sharedTokenSources.LoadOrStore(key, newTokenSource(creds, doer))
	return v.(*tokenSource)
}
