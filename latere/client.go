// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	managed "latere.ai/x/managed-agents"
)

// maxResponseBytes bounds what one call to the platform may return, so a
// misbehaving server cannot exhaust memory. A session file read is bounded
// separately, by the platform's own per-file limit.
const maxResponseBytes = 64 << 20

// apiError is the platform's error body, {"error": {"code", "message"}}.
// Only the code and message are kept: the details can echo request fields,
// and an error is logged.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("platform answered %d", e.Status)
	}
	return fmt.Sprintf("platform answered %d %s: %s", e.Status, e.Code, e.Message)
}

// client is the adapter's one HTTP client for the platform's API: Topos under
// /v1/agents, the account under /v1/account, the models catalog under
// /v1/models and Storage under /v1/storage, all on one origin and all taking
// the same bearer.
type client struct {
	base string
	http *http.Client
}

// call sends one request and decodes a JSON answer into out when out is not
// nil. A 404 is managed.NotFoundError, a 429 or 5xx a temporary error that
// honors Retry-After, and any other failure an *apiError. The bearer never
// appears in an error.
func (c *client) call(ctx context.Context, bearer, method, path string, body any, idempotencyKey string, out any) error {
	raw, err := c.send(ctx, bearer, method, path, body, idempotencyKey)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, redactPath(path), err)
	}
	return nil
}

// send is call without the decoding, for a route whose answer is bytes.
func (c *client) send(ctx context.Context, bearer, method, path string, body any, idempotencyKey string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s: %w", method, redactPath(path), err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		// A transport failure is retryable: the request may not have
		// reached the platform at all, and every mutation carries an
		// idempotency key, so a retry cannot apply it twice.
		return nil, &managed.TemporaryError{Err: fmt.Errorf("%s %s: %w", method, redactPath(path), err)}
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &managed.TemporaryError{Err: fmt.Errorf("read %s %s: %w", method, redactPath(path), err)}
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("%s %s: the answer is larger than %d bytes", method, redactPath(path), maxResponseBytes)
	}
	if response.StatusCode/100 == 2 {
		return raw, nil
	}
	failure := &apiError{Status: response.StatusCode}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		failure.Code, failure.Message = envelope.Error.Code, envelope.Error.Message
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, errors.Join(&managed.NotFoundError{Runtime: Name, ID: redactPath(path)}, failure)
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return nil, &managed.TemporaryError{Err: failure, After: retryAfter(response.Header.Get("Retry-After"))}
	default:
		return nil, failure
	}
}

// retryAfter reads a Retry-After header given in seconds. The HTTP-date form
// is not used by the platform and reads as no hint.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// redactPath drops a query string from a path before it is put in an error,
// since a query can carry a file path a person chose.
func redactPath(path string) string {
	if before, _, ok := strings.Cut(path, "?"); ok {
		return before
	}
	return path
}

// escape is url.PathEscape for one path segment.
func escape(segment string) string { return url.PathEscape(segment) }

// code reports the platform's error code of err, or "" when err is not an
// answer from the platform.
func code(err error) string {
	var failure *apiError
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

// defaultHTTPClient is the client every call goes through when the caller
// gives none, with a timeout long enough for a large file read from Storage.
// A caller that traces its outbound calls passes its own in Config.HTTP.
func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}
