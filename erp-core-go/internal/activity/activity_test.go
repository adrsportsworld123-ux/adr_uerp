package activity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactedBodyMasksCredentials(t *testing.T) {
	in := `{"email":"a@b.in","password":"hunter2","approval":{"manager_pin":"1234","shipping":"x"},
		"lines":[{"refresh_token":"t","qty":1}],"pin":"9999","otp":"1","new_password":"p","api_key":"k"}`
	out := string(redactedBody([]byte(in), len(in), false, "application/json; charset=utf-8"))
	for _, secret := range []string{"hunter2", "1234", `"t"`, "9999", `"p"`, `"k"`} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %s survived redaction: %s", secret, out)
		}
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v["email"] != "a@b.in" || v["approval"].(map[string]any)["shipping"] != "x" {
		t.Fatalf("non-secret fields must be kept: %s", out)
	}
}

func TestRedactedBodyPlaceholders(t *testing.T) {
	if b := redactedBody(nil, maxBody+1, true, "application/json"); !strings.Contains(string(b), "_omitted") {
		t.Fatalf("truncated body: %s", b)
	}
	if b := redactedBody([]byte("a,b"), 3, false, "text/csv"); !strings.Contains(string(b), "non-JSON") {
		t.Fatalf("csv body: %s", b)
	}
	if b := redactedBody([]byte("{"), 1, false, "application/json"); !strings.Contains(string(b), "malformed") {
		t.Fatalf("bad json: %s", b)
	}
	if b := redactedBody(nil, 0, false, "application/json"); b != nil {
		t.Fatalf("empty body should store nothing: %s", b)
	}
}

func TestModuleOf(t *testing.T) {
	for route, want := range map[string]string{
		"/sales/orders/{id}/checkout": "sales", "/hr/attendance/clock-in": "hr", "/": "other", "/auth/login": "auth",
	} {
		if got := moduleOf(route); got != want {
			t.Errorf("moduleOf(%q) = %q, want %q", route, got, want)
		}
	}
}
