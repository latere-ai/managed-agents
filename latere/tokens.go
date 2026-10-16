// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// TokenSource gives a bearer for the platform's API that acts as one person.
// Every call asks for one, so a source may refresh whenever it needs to; a
// bearer is never stored in a reference, an event or a log.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// TokenFunc adapts a function to a TokenSource.
type TokenFunc func(ctx context.Context) (string, error)

// Token calls the function.
func (f TokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type tokenSourceKey struct{}

// WithTokenSource attaches the token source of the person a call acts for.
// A server that runs many people's sessions through one runtime passes each
// session's source this way; a source on the context wins over the
// runtime's own.
func WithTokenSource(ctx context.Context, source TokenSource) context.Context {
	return context.WithValue(ctx, tokenSourceKey{}, source)
}

func tokenSourceFrom(ctx context.Context) (TokenSource, bool) {
	source, ok := ctx.Value(tokenSourceKey{}).(TokenSource)
	return source, ok && source != nil
}

// ErrNoCredential is a call that has no person to act for: no source on the
// context and none configured on the runtime.
var ErrNoCredential = errors.New("no Latere credential: sign in to Latere, or give the runtime a personal access token")

// renewBefore is how long before its expiry a cached bearer is replaced, so
// a bearer handed to a call does not expire while the call is in flight.
const renewBefore = time.Minute

// cache keeps one bearer until shortly before it expires.
type cache struct {
	mu     sync.Mutex
	bearer string
	until  time.Time
	now    func() time.Time
	mint   func(ctx context.Context) (string, time.Time, error)
}

// Token gives the kept bearer while it is fresh, and mints a new one when it
// is due.
func (c *cache) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.bearer != "" && now.Before(c.until.Add(-renewBefore)) {
		return c.bearer, nil
	}
	bearer, until, err := c.mint(ctx)
	if err != nil {
		return "", err
	}
	c.bearer, c.until = bearer, until
	return bearer, nil
}

// PersonalAccessToken exchanges a person's personal access token (pat_...)
// at Latere Auth for a fifteen-minute bearer addressed to api.latere.ai, the
// way the platform's documentation describes, and keeps it until it is due.
// The key itself never leaves this process except to Auth. A nil client is
// the runtime's default one.
func PersonalAccessToken(authURL, key string, httpClient *http.Client) TokenSource {
	authURL = strings.TrimRight(authURL, "/")
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	c := &cache{now: time.Now}
	c.mint = func(ctx context.Context) (string, time.Time, error) {
		form := url.Values{"grant_type": {"client_credentials"}}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/token", strings.NewReader(form.Encode()))
		if err != nil {
			return "", time.Time{}, err
		}
		request.SetBasicAuth("pat", key)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := httpClient.Do(request)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("exchange the personal access token: %w", err)
		}
		defer func() { _ = response.Body.Close() }()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("exchange the personal access token: %w", err)
		}
		if response.StatusCode/100 != 2 {
			return "", time.Time{}, fmt.Errorf("exchange the personal access token: Auth answered %d", response.StatusCode)
		}
		var body struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.AccessToken == "" {
			return "", time.Time{}, errors.New("exchange the personal access token: Auth gave no access token")
		}
		lifetime := time.Duration(body.ExpiresIn) * time.Second
		if lifetime <= 0 {
			lifetime = 15 * time.Minute
		}
		return body.AccessToken, c.now().Add(lifetime), nil
	}
	return c
}

// commandLifetime is how long a bearer printed by a command is kept. The
// command is asked again after it, so a command whose bearers live five
// minutes, as an actor token does, is never handed back an expired one.
const commandLifetime = 4 * time.Minute

// Command runs an operator's command that prints a bearer for api.latere.ai
// on its standard output, in the manner of a git credential helper, and
// keeps what it printed for a few minutes. It is how a person who is signed
// in with another Latere program lends that sign-in to this one.
func Command(command string) TokenSource {
	c := &cache{now: time.Now}
	c.mint = func(ctx context.Context) (string, time.Time, error) {
		//nolint:gosec // the operator's own configured command, run as the operator
		output, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
		if err != nil {
			return "", time.Time{}, fmt.Errorf("the Latere token command failed: %w", err)
		}
		bearer := strings.TrimSpace(string(output))
		if bearer == "" || strings.ContainsAny(bearer, " \n\t") {
			return "", time.Time{}, errors.New("the Latere token command printed no single bearer")
		}
		return bearer, c.now().Add(commandLifetime), nil
	}
	return c
}
