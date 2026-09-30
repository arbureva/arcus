package mimo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError is returned for any non-2xx response. It implements error.
//
// The official docs list status codes but not the error body; in practice it
// is the OpenAI envelope, and gateways that host MiMo (e.g. Tencent TokenHub)
// send Code as a number. Anything else lands verbatim in Message.
type APIError struct {
	StatusCode int    `json:"-"`
	RequestID  string `json:"-"`
	RetryAfter time.Duration

	Type    string   `json:"type"`
	Code    anyValue `json:"code"`
	Param   string   `json:"param"`
	Message string   `json:"message"`
}

func (e *APIError) Error() string {
	id := ""
	if e.RequestID != "" {
		id = " (request-id: " + e.RequestID + ")"
	}
	return fmt.Sprintf("mimo: %d %s/%s: %s%s", e.StatusCode, e.Type, e.Code, e.Message, id)
}

// Retryable reports whether the request may be retried: 429 (rate limit),
// 408, and 5xx. 402 (insufficient balance) and 421 (content filter) are final.
func (e *APIError) Retryable() bool {
	switch {
	case e.StatusCode == http.StatusTooManyRequests:
		return true
	case e.StatusCode == http.StatusRequestTimeout:
		return true
	case e.StatusCode >= 500:
		return true
	default:
		return false
	}
}

// anyValue decodes a JSON string or number into its textual form.
type anyValue string

func (v *anyValue) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*v = anyValue(s)
		return nil
	}
	*v = anyValue(strings.Trim(string(b), `"`))
	if *v == "null" {
		*v = ""
	}
	return nil
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

// parseAPIError builds an *APIError from a non-2xx response without closing it.
func parseAPIError(resp *http.Response) *APIError {
	e := &APIError{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("X-Request-Id"),
		RetryAfter: parseRetryAfter(resp.Header),
		Message:    http.StatusText(resp.StatusCode),
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if len(body) == 0 {
		return e
	}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && (env.Error.Message != "" || env.Error.Type != "") {
		env.Error.StatusCode = e.StatusCode
		env.Error.RequestID = e.RequestID
		env.Error.RetryAfter = e.RetryAfter
		return &env.Error
	}
	e.Message = string(body)
	return e
}
