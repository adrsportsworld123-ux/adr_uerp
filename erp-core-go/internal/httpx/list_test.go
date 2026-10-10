package httpx

import (
	"net/http/httptest"
	"testing"
)

var sorts = map[string]string{"name": "t.name", "updated": "t.updated_at"}

func TestParseListParamsUnpagedByDefault(t *testing.T) {
	p := ParseListParams(httptest.NewRequest("GET", "/x?q=%20bat%20", nil), sorts, "name")
	if p.Paged {
		t.Fatal("no page/limit given: must stay unpaged so existing dropdown callers get every row")
	}
	if limit, offset := p.SQLLimit(); limit != nil || offset != 0 {
		t.Fatalf("unpaged SQLLimit = %v, %d; want nil, 0", limit, offset)
	}
	if p.Q != "bat" {
		t.Fatalf("q not trimmed: %q", p.Q)
	}
	if p.OrderBy != "t.name ASC NULLS LAST" {
		t.Fatalf("default order = %q", p.OrderBy)
	}
	env := p.Envelope("items", []int{1, 2, 3}, 3)
	if env["page"] != 1 || env["limit"] != 3 || env["total"] != 3 {
		t.Fatalf("unpaged envelope = %v", env)
	}
}

func TestParseListParamsPaging(t *testing.T) {
	p := ParseListParams(httptest.NewRequest("GET", "/x?page=3&limit=10&sort=updated&dir=DESC", nil), sorts, "name")
	if !p.Paged || p.Page != 3 || p.Limit != 10 || p.Offset != 20 {
		t.Fatalf("got %+v", p)
	}
	if p.OrderBy != "t.updated_at DESC NULLS LAST" {
		t.Fatalf("order = %q", p.OrderBy)
	}
}

func TestParseListParamsClampsAndRejectsUnsafeSort(t *testing.T) {
	p := ParseListParams(httptest.NewRequest("GET", "/x?limit=5000&sort=name;DROP%20TABLE%20x", nil), sorts, "name")
	if p.Limit != MaxPageSize {
		t.Fatalf("limit not clamped: %d", p.Limit)
	}
	// An unknown sort key never reaches SQL: it falls back to the default.
	if p.OrderBy != "t.name ASC NULLS LAST" {
		t.Fatalf("unknown sort leaked into ORDER BY: %q", p.OrderBy)
	}
	bad := ParseListParams(httptest.NewRequest("GET", "/x?page=-2&limit=0", nil), sorts, "name")
	if bad.Paged || bad.Page != 1 {
		t.Fatalf("invalid page/limit should be ignored: %+v", bad)
	}
}
