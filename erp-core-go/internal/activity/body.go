package activity

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxBody is how much of a request body the log keeps. Larger bodies
// (a bank statement CSV, a logo) are recorded by size only.
const maxBody = 16 << 10

// peekBody reads up to maxBody+1 bytes and puts them back in front of the
// rest of the body, so the handler still reads the whole thing.
func peekBody(r *http.Request) (head []byte, n int, truncated bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, 0, false
	}
	head, _ = io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if len(head) > maxBody {
		return nil, len(head), true
	}
	return head, len(head), false
}

// redactedBody is the JSON the log stores for a request body: the body
// itself with every credential-like field masked, or a small placeholder
// when it isn't JSON or was too large to keep.
func redactedBody(head []byte, n int, truncated bool, contentType string) []byte {
	switch {
	case truncated:
		b, _ := json.Marshal(map[string]any{"_omitted": "body larger than 16 KB", "_bytes_at_least": n})
		return b
	case n == 0:
		return nil
	case !strings.Contains(strings.ToLower(contentType), "json"):
		b, _ := json.Marshal(map[string]any{"_omitted": "non-JSON body", "_content_type": contentType, "_bytes": n})
		return b
	}
	var v any
	if err := json.Unmarshal(head, &v); err != nil {
		b, _ := json.Marshal(map[string]any{"_omitted": "malformed JSON", "_bytes": n})
		return b
	}
	b, err := json.Marshal(redact(v))
	if err != nil {
		return nil
	}
	return b
}

func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveKey(k) {
				t[k] = "[redacted]"
			} else {
				t[k] = redact(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = redact(t[i])
		}
		return t
	}
	return v
}

// sensitiveKey: passwords, PINs (an approver's PIN rides along on
// discount and variance approvals), tokens, OTPs, secrets and card data.
// Exact or suffix matches for the short ones, so "shipping" isn't a PIN.
func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "secret", "token", "cvv", "card_number", "authorization", "api_key"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	for _, s := range []string{"pin", "otp", "passcode", "pairing_code", "code_hash"} {
		if k == s || strings.HasSuffix(k, "_"+s) {
			return true
		}
	}
	return false
}
