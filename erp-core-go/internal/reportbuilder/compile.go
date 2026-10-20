package reportbuilder

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// Spec is a report: what a template saves and what a run executes.
type Spec struct {
	Dataset  string   `json:"dataset"`
	Columns  []Column `json:"columns"`
	Filters  []Filter `json:"filters"`
	Sort     []Sort   `json:"sort"`
	DateFrom string   `json:"date_from,omitempty"` // YYYY-MM-DD, inclusive
	DateTo   string   `json:"date_to,omitempty"`
	// Period: a range relative to the day it runs ("this_month",
	// "last_week" ... see Periods), used when DateFrom and DateTo are both
	// empty, so a saved report always covers the current month, week etc.
	Period string `json:"period,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	// clock: the merchant's business time zone a Period is resolved in
	// (migrations/064); set by the handlers and the runner, never stored.
	// nil = IST, as before.
	clock *time.Location
}

// now is the current time on the spec's clock.
func (s Spec) now() time.Time {
	if s.clock != nil {
		return time.Now().In(s.clock)
	}
	return time.Now().In(istZone)
}

type Column struct {
	Field string `json:"field"`
	Agg   string `json:"agg,omitempty"` // sum | avg | min | max | count | count_distinct
	Label string `json:"label,omitempty"`
}

type Filter struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

type Sort struct {
	Column int  `json:"column"` // index into Columns
	Desc   bool `json:"desc"`
}

// Cost limits: a spec can't ask for more than these, whoever runs it.
const (
	MaxDateSpanDays = 366
	MaxFilters      = 20
	PreviewRows     = 1000
	ExportRows      = 100000
	maxInValues     = 100
	maxTextValue    = 200
)

var ErrInvalid = errors.New("invalid report")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// RetentionError: the date range starts before the plan's report history.
type RetentionError struct{ Earliest time.Time }

func (e *RetentionError) Error() string {
	return "the plan's reports reach back to " + e.Earliest.Format("2006-01-02")
}

// ColumnMeta describes a result column for the client.
type ColumnMeta struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Agg   string `json:"agg,omitempty"`
}

var aggLabel = map[string]string{"sum": "Total", "avg": "Average", "min": "Lowest", "max": "Highest", "count": "Count of", "count_distinct": "Distinct"}

// Compiled is a validated spec ready to run.
type Compiled struct {
	SQL     string
	Args    []any
	Columns []ColumnMeta
}

func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

// Compile validates spec against the allow-list and builds one
// parameterised SELECT. horizon (zero = none) is the plan's earliest
// reportable date; rowCap bounds the LIMIT.
func Compile(spec Spec, horizon time.Time, rowCap int) (Compiled, error) {
	ds, ok := FindDataset(spec.Dataset)
	if !ok {
		return Compiled{}, invalid("unknown dataset %q", spec.Dataset)
	}
	if len(spec.Columns) == 0 || len(spec.Columns) > maxFields {
		return Compiled{}, invalid("choose 1 to %d columns", maxFields)
	}
	if len(spec.Filters) > MaxFilters {
		return Compiled{}, invalid("at most %d filters", MaxFilters)
	}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	var sel, group []string
	var meta []ColumnMeta
	hasAgg := false
	for i, c := range spec.Columns {
		f, ok := ds.Field(c.Field)
		if !ok {
			return Compiled{}, invalid("column %d: unknown field %q", i+1, c.Field)
		}
		m := ColumnMeta{Key: f.Key, Label: f.Label, Type: f.Type}
		expr := f.SQL
		if c.Agg != "" {
			if !contains(f.Aggs(), c.Agg) {
				return Compiled{}, invalid("%s can't be aggregated with %s", f.Label, c.Agg)
			}
			hasAgg = true
			switch c.Agg {
			case "count":
				expr = "COUNT(" + f.SQL + ")"
				m.Type = TNumber
			case "count_distinct":
				expr = "COUNT(DISTINCT " + f.SQL + ")"
				m.Type = TNumber
			case "avg":
				expr = "round(AVG(" + f.SQL + "), 2)"
			default:
				expr = strings.ToUpper(c.Agg) + "(" + f.SQL + ")"
			}
			m.Agg = c.Agg
			m.Label = aggLabel[c.Agg] + " " + lowerLabel(f.Label)
		} else {
			group = append(group, strconv.Itoa(i+1))
		}
		if strings.TrimSpace(c.Label) != "" {
			m.Label = strings.TrimSpace(c.Label)
		}
		sel = append(sel, fmt.Sprintf("(%s) AS c%d", expr, i))
		meta = append(meta, m)
	}

	where := []string{"(" + ds.where + ")"}
	if ds.HasDateRange && spec.DateFrom == "" && spec.DateTo == "" && spec.Period != "" {
		from, to, ok := ResolvePeriod(spec.Period, spec.now())
		if !ok {
			return Compiled{}, invalid("unknown period %q", spec.Period)
		}
		spec.DateFrom, spec.DateTo = from, to
	}
	if ds.HasDateRange {
		from, err1 := parseDate(spec.DateFrom)
		to, err2 := parseDate(spec.DateTo)
		if err1 != nil || err2 != nil {
			return Compiled{}, invalid("date_from and date_to (YYYY-MM-DD) are required for %s", ds.Label)
		}
		if to.Before(from) {
			return Compiled{}, invalid("date_to is before date_from")
		}
		if to.Sub(from) > MaxDateSpanDays*24*time.Hour {
			return Compiled{}, invalid("a report covers at most %d days", MaxDateSpanDays)
		}
		if !ds.Statutory && !horizon.IsZero() && from.Before(time.Date(horizon.Year(), horizon.Month(), horizon.Day(), 0, 0, 0, 0, time.UTC)) {
			return Compiled{}, &RetentionError{Earliest: horizon}
		}
		where = append(where, fmt.Sprintf("%s BETWEEN %s::date AND %s::date", ds.date, arg(spec.DateFrom), arg(spec.DateTo)))
	}
	for i, fl := range spec.Filters {
		f, ok := ds.Field(fl.Field)
		if !ok {
			return Compiled{}, invalid("filter %d: unknown field %q", i+1, fl.Field)
		}
		cond, err := filterSQL(f, fl, arg)
		if err != nil {
			return Compiled{}, invalid("filter on %s: %v", f.Label, err)
		}
		where = append(where, cond)
	}

	var order []string
	for _, s := range spec.Sort {
		if s.Column < 0 || s.Column >= len(spec.Columns) {
			return Compiled{}, invalid("sort column %d doesn't exist", s.Column+1)
		}
		dir := "ASC"
		if s.Desc {
			dir = "DESC"
		}
		// Sort on the typed expression, not its text cast (numbers sort as numbers).
		order = append(order, fmt.Sprintf("%d %s NULLS LAST", s.Column+1, dir))
	}
	if len(order) == 0 {
		order = []string{"1"}
	}
	limit := spec.Limit
	if limit <= 0 || limit > rowCap {
		limit = rowCap
	}

	// The inner query keeps native types for grouping and sorting; the outer
	// one returns text, so NUMERIC money stays exact end to end.
	outer := make([]string, len(spec.Columns))
	for i := range outer {
		outer[i] = fmt.Sprintf("q.c%d::text", i)
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM " + ds.from + " WHERE " + strings.Join(where, " AND ")
	if hasAgg && len(group) > 0 {
		q += " GROUP BY " + strings.Join(group, ", ")
	}
	q += " ORDER BY " + strings.Join(order, ", ") + " LIMIT " + strconv.Itoa(limit+1) // one extra row says "truncated"
	return Compiled{SQL: "SELECT " + strings.Join(outer, ", ") + " FROM (" + q + ") q", Args: args, Columns: meta}, nil
}

var ops = map[string][]string{
	TText:   {"eq", "ne", "in", "not_in", "contains", "empty", "not_empty"},
	TNumber: {"eq", "ne", "gt", "gte", "lt", "lte", "between", "empty", "not_empty"},
	TMoney:  {"eq", "ne", "gt", "gte", "lt", "lte", "between", "empty", "not_empty"},
	TDate:   {"eq", "gt", "gte", "lt", "lte", "between"},
	TBool:   {"is_true", "is_false"},
}

// OpsFor lists the filter operators a field type takes.
func OpsFor(t string) []string { return ops[t] }

func filterSQL(f Field, fl Filter, arg func(any) string) (string, error) {
	if !contains(ops[f.Type], fl.Op) {
		return "", fmt.Errorf("operator %q doesn't apply", fl.Op)
	}
	cast := map[string]string{TText: "text", TNumber: "numeric", TMoney: "numeric", TDate: "date"}[f.Type]
	one := func(v any) (string, error) {
		val, err := scalar(f.Type, v)
		if err != nil {
			return "", err
		}
		return arg(val) + "::" + cast, nil
	}
	sqlOp := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
	switch fl.Op {
	case "empty":
		return "(" + f.SQL + ") IS NULL", nil
	case "not_empty":
		return "(" + f.SQL + ") IS NOT NULL", nil
	case "is_true":
		return "(" + f.SQL + ") IS TRUE", nil
	case "is_false":
		return "(" + f.SQL + ") IS NOT TRUE", nil
	case "contains":
		s, ok := fl.Value.(string)
		if !ok || s == "" || len(s) > maxTextValue {
			return "", errors.New("contains needs some text")
		}
		return "(" + f.SQL + ") ILIKE '%' || " + arg(escapeLike(s)) + " || '%'", nil
	case "in", "not_in":
		list, ok := fl.Value.([]any)
		if !ok || len(list) == 0 || len(list) > maxInValues {
			return "", fmt.Errorf("needs a list of 1 to %d values", maxInValues)
		}
		vals := make([]string, 0, len(list))
		for _, v := range list {
			s, ok := v.(string)
			if !ok || len(s) > maxTextValue {
				return "", errors.New("list values must be text")
			}
			vals = append(vals, s)
		}
		neg := ""
		if fl.Op == "not_in" {
			neg = "NOT "
		}
		return "(" + f.SQL + ") " + neg + "IN (SELECT unnest(" + arg(vals) + "::text[]))", nil
	case "between":
		pair, ok := fl.Value.([]any)
		if !ok || len(pair) != 2 {
			return "", errors.New("between needs [from, to]")
		}
		a, err := one(pair[0])
		if err != nil {
			return "", err
		}
		b, err := one(pair[1])
		if err != nil {
			return "", err
		}
		return "(" + f.SQL + ") BETWEEN " + a + " AND " + b, nil
	default:
		v, err := one(fl.Value)
		if err != nil {
			return "", err
		}
		return "(" + f.SQL + ") " + sqlOp[fl.Op] + " " + v, nil
	}
}

func scalar(t string, v any) (any, error) {
	switch t {
	case TNumber, TMoney:
		switch n := v.(type) {
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64), nil
		case string:
			if _, err := strconv.ParseFloat(n, 64); err == nil {
				return n, nil
			}
		}
		return nil, errors.New("needs a number")
	case TDate:
		if s, ok := v.(string); ok {
			if _, err := parseDate(s); err == nil {
				return s, nil
			}
		}
		return nil, errors.New("needs a date (YYYY-MM-DD)")
	default:
		if s, ok := v.(string); ok && len(s) <= maxTextValue {
			return s, nil
		}
		return nil, errors.New("needs text")
	}
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Result is a run's output: every cell is text (or null).
type Result struct {
	Columns   []ColumnMeta `json:"columns"`
	Rows      [][]*string  `json:"rows"`
	Truncated bool         `json:"truncated"`
}

// Execute runs a compiled report in tx with a statement timeout.
func Execute(ctx context.Context, tx pgx.Tx, c Compiled, rowCap int, timeout time.Duration) (Result, error) {
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return Result{}, err
	}
	rows, err := tx.Query(ctx, c.SQL, c.Args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	res := Result{Columns: c.Columns, Rows: [][]*string{}}
	for rows.Next() {
		if len(res.Rows) == rowCap {
			res.Truncated = true
			break
		}
		cells := make([]*string, len(c.Columns))
		ptrs := make([]any, len(cells))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		res.Rows = append(res.Rows, cells)
	}
	return res, rows.Err()
}

// lowerLabel lower-cases a field label for "Total …", except an acronym
// (CGST, IGST, HSN), which stays as it is.
func lowerLabel(l string) string {
	if l == strings.ToUpper(l) {
		return l
	}
	return strings.ToLower(l)
}
