package reportbuilder

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func spec(cols ...Column) Spec {
	return Spec{Dataset: "sales_items", Columns: cols, DateFrom: "2026-10-01", DateTo: "2026-10-31"}
}

func TestCompileGroupsAndParameterises(t *testing.T) {
	s := spec(Column{Field: "category"}, Column{Field: "line_total", Agg: "sum"})
	s.Filters = []Filter{{Field: "branch", Op: "in", Value: []any{"MG Road", "x'); DROP TABLE users; --"}}, {Field: "quantity", Op: "gte", Value: 2.0}}
	s.Sort = []Sort{{Column: 1, Desc: true}}
	c, err := Compile(s, time.Time{}, PreviewRows)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GROUP BY 1", "SUM(l.line_total)", "ORDER BY 2 DESC NULLS LAST", "LIMIT 1001", "unnest($3::text[])", ">= $4::numeric"} {
		if !strings.Contains(c.SQL, want) {
			t.Errorf("missing %q in %s", want, c.SQL)
		}
	}
	if strings.Contains(c.SQL, "DROP TABLE") {
		t.Error("a value reached the SQL text")
	}
	if len(c.Args) != 4 || c.Columns[1].Label != "Total line total" || c.Columns[1].Type != TMoney {
		t.Errorf("args %v cols %+v", c.Args, c.Columns)
	}
}

func TestCompileRefuses(t *testing.T) {
	horizon := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]Spec{
		"unknown dataset":         {Dataset: "users", Columns: []Column{{Field: "name"}}},
		"unknown field":           spec(Column{Field: "so.id; DROP TABLE x"}),
		"no columns":              spec(),
		"bad aggregation":         spec(Column{Field: "category", Agg: "sum"}),
		"no date range":           {Dataset: "sales", Columns: []Column{{Field: "branch"}}},
		"backwards range":         {Dataset: "sales", Columns: []Column{{Field: "branch"}}, DateFrom: "2026-10-31", DateTo: "2026-10-01"},
		"over a year":             {Dataset: "sales", Columns: []Column{{Field: "branch"}}, DateFrom: "2025-01-01", DateTo: "2026-10-01"},
		"text op on a number":     {Dataset: "sales", Columns: []Column{{Field: "branch"}}, DateFrom: "2026-10-01", DateTo: "2026-10-02", Filters: []Filter{{Field: "grand_total", Op: "contains", Value: "1"}}},
		"number filter with text": {Dataset: "sales", Columns: []Column{{Field: "branch"}}, DateFrom: "2026-10-01", DateTo: "2026-10-02", Filters: []Filter{{Field: "grand_total", Op: "gt", Value: "1; DROP"}}},
		"sort out of range":       {Dataset: "sales", Columns: []Column{{Field: "branch"}}, DateFrom: "2026-10-01", DateTo: "2026-10-02", Sort: []Sort{{Column: 3}}},
	}
	for name, s := range cases {
		if _, err := Compile(s, time.Time{}, PreviewRows); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	var re *RetentionError
	if _, err := Compile(spec(Column{Field: "sku"}), horizon.AddDate(0, 1, 15), PreviewRows); !errors.As(err, &re) {
		t.Errorf("before the retention horizon: got %v", err)
	}
	if _, err := Compile(Spec{Dataset: "stock", Columns: []Column{{Field: "sku"}}}, horizon, PreviewRows); err != nil {
		t.Errorf("stock needs no date range: %v", err)
	}
}

func TestEveryFieldCompiles(t *testing.T) {
	for _, d := range Datasets {
		for _, f := range d.Fields {
			s := Spec{Dataset: d.Key, Columns: []Column{{Field: f.Key}, {Field: f.Key, Agg: "count"}}, DateFrom: "2026-10-01", DateTo: "2026-10-02"}
			if _, err := Compile(s, time.Time{}, 10); err != nil {
				t.Errorf("%s.%s: %v", d.Key, f.Key, err)
			}
		}
	}
}

func TestExports(t *testing.T) {
	v := func(s string) *string { return &s }
	r := Result{Columns: []ColumnMeta{{Label: "Product", Type: TText}, {Label: "Total", Type: TMoney}},
		Rows: [][]*string{{v("=HYPERLINK(\"x\")"), v("-12.50")}, {v("Bat <pro> & co"), nil}}}
	csv, err := CSV(r)
	if err != nil || !strings.Contains(string(csv), "'=HYPERLINK") || !strings.Contains(string(csv), ",-12.50") {
		t.Errorf("csv %q %v", csv, err)
	}
	x, err := XLSX(r, "Sales: by [item]")
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(x), int64(len(x)))
	if err != nil || len(z.File) != 6 {
		t.Fatalf("xlsx zip: %v", err)
	}
	for _, f := range z.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			var b bytes.Buffer
			_, _ = b.ReadFrom(rc)
			body := b.String()
			if !strings.Contains(body, `<c r="B2" s="2"><v>-12.50</v>`) || !strings.Contains(body, "Bat &lt;pro&gt; &amp; co") {
				t.Errorf("sheet: %s", body)
			}
		}
	}
	if colName(0) != "A" || colName(25) != "Z" || colName(26) != "AA" || colName(701) != "ZZ" {
		t.Error("column names")
	}
}
