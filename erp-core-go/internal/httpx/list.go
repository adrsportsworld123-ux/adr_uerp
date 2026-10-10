package httpx

import (
	"net/http"
	"strconv"
	"strings"
)

// ListParams is the Phase 11 list convention (phased_roadmap.md Phase 11):
// every master list endpoint accepts
//
//	?q=&status=&sort=<field>&dir=asc|desc&page=&limit=
//
// and answers with its existing collection key plus `total`, `page` and
// `limit`, so a table can show "Showing 1–25 of 1,284" and page on the
// server. Additive by design: an endpoint that returned everything before
// still returns everything when neither page nor limit is passed (Paged ==
// false), so existing dropdown callers keep working unchanged.
type ListParams struct {
	Q      string
	Status string
	Page   int
	Limit  int
	Offset int
	Paged  bool
	// OrderBy is a trusted SQL fragment (column + direction) chosen from the
	// caller's allow-list — never request text.
	OrderBy string
}

const (
	DefaultPageSize = 25
	MaxPageSize     = 200
)

// ParseListParams reads the list query string. sorts maps the public sort
// key to its SQL column; defaultSort must be one of its keys. An unknown
// sort key falls back to the default rather than erroring, so a stale
// bookmark still loads.
func ParseListParams(r *http.Request, sorts map[string]string, defaultSort string) ListParams {
	q := r.URL.Query()
	p := ListParams{
		Q:      strings.TrimSpace(q.Get("q")),
		Status: strings.TrimSpace(q.Get("status")),
		Page:   1,
		Limit:  DefaultPageSize,
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		p.Limit = min(v, MaxPageSize)
		p.Paged = true
	}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		p.Page = v
		p.Paged = true
	}
	p.Offset = (p.Page - 1) * p.Limit

	col, ok := sorts[q.Get("sort")]
	if !ok {
		col = sorts[defaultSort]
	}
	dir := "ASC"
	if strings.EqualFold(q.Get("dir"), "desc") {
		dir = "DESC"
	}
	p.OrderBy = col + " " + dir + " NULLS LAST"
	return p
}

// SQLLimit returns the LIMIT/OFFSET pair to bind: everything (NULL limit)
// when the request isn't paged.
func (p ListParams) SQLLimit() (limit any, offset int) {
	if !p.Paged {
		return nil, 0
	}
	return p.Limit, p.Offset
}

// Envelope adds the paging fields next to an endpoint's own collection key.
func (p ListParams) Envelope(key string, items any, total int) map[string]any {
	page, limit := p.Page, p.Limit
	if !p.Paged {
		page, limit = 1, total
	}
	return map[string]any{key: items, "total": total, "page": page, "limit": limit}
}
