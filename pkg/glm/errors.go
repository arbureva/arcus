package glm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIError is returned for any non-2xx response. It implements error.
//
// GLM's body is {"error":{"code":"1301","message":"..."}}: Code is a string
// business code layered on top of the HTTP status (e.g. 1211 model not found,
// 1261 prompt too long, 1301 sensitive content, 1113 account in arrears).
type APIError struct {
	StatusCode int    `json:"-"`
	RequestID  string `json:"-"`
	RetryAfter time.Duration

	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	id := ""
	if e.RequestID != "" {
		id = " (request-id: " + e.RequestID + ")"
	}
	return fmt.Sprintf("glm: %d/%s: %s%s", e.StatusCode, e.Code, e.Message, id)
}

// Retryable reports whether the request may be retried: 408, 5xx, and 429 —
// except the 429s that no amount of waiting fixes (1113 arrears, 1308 usage
// quota reached).
func (e *APIError) Retryable() bool {
	switch {
	case e.StatusCode == http.StatusTooManyRequests:
		return e.Code != "1113" && e.Code != "1308"
	case e.StatusCode == http.StatusRequestTimeout:
		return true
	case e.StatusCode >= 500:
		return true
	default:
		return false
	}
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
	if err := json.Unmarshal(body, &env); err == nil && (env.Error.Message != "" || env.Error.Code != "") {
		env.Error.StatusCode = e.StatusCode
		env.Error.RequestID = e.RequestID
		env.Error.RetryAfter = e.RetryAfter
		return &env.Error
	}
	e.Message = string(body)
	return e
}
