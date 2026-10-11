package reportbuilder

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"strings"
)

// CSV writes the header row and every row.
func CSV(r Result) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\ufeff") // so Excel opens UTF-8 (₹, names) correctly
	w := csv.NewWriter(&buf)
	head := make([]string, len(r.Columns))
	for i, c := range r.Columns {
		head[i] = c.Label
	}
	if err := w.Write(head); err != nil {
		return nil, err
	}
	for _, row := range r.Rows {
		rec := make([]string, len(row))
		for i, v := range row {
			if v != nil {
				rec[i] = csvSafe(*v)
			}
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// csvSafe neutralises spreadsheet formula injection: a cell a user typed
// (a product name, a reason) that starts with = + - @ would otherwise run
// as a formula when the file is opened.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		if _, err := fmt.Sscanf(s, "%g", new(float64)); err == nil {
			return s // a negative number, not a formula
		}
		return "'" + s
	}
	return s
}

// XLSX writes a one-sheet workbook: numbers and money as numeric cells,
// everything else as text (inline strings, so there is nothing to execute).
// A minimal SpreadsheetML package with the standard library only.
func XLSX(r Result, sheet string) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	put := func(name, body string) error {
		w, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	const hdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	files := []struct{ name, body string }{
		{"[Content_Types].xml", hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
			`</Types>`},
		{"_rels/.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`},
		{"xl/workbook.xml", hdr + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + xmlEscape(sheetName(sheet)) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`</Relationships>`},
		// Style 1: bold header; style 2: two-decimal money.
		{"xl/styles.xml", hdr + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<numFmts count="1"><numFmt numFmtId="164" formatCode="#,##0.00"/></numFmts>` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
			`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
			`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs>` +
			`</styleSheet>`},
	}
	for _, f := range files {
		if err := put(f.name, f.body); err != nil {
			return nil, err
		}
	}
	var s strings.Builder
	s.WriteString(hdr + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	s.WriteString(`<row r="1">`)
	for i, c := range r.Columns {
		fmt.Fprintf(&s, `<c r="%s1" t="inlineStr" s="1"><is><t>%s</t></is></c>`, colName(i), xmlEscape(c.Label))
	}
	s.WriteString(`</row>`)
	for ri, row := range r.Rows {
		n := ri + 2
		fmt.Fprintf(&s, `<row r="%d">`, n)
		for i, v := range row {
			if v == nil {
				continue
			}
			ref := fmt.Sprintf("%s%d", colName(i), n)
			switch r.Columns[i].Type {
			case TMoney:
				fmt.Fprintf(&s, `<c r="%s" s="2"><v>%s</v></c>`, ref, xmlEscape(*v))
			case TNumber:
				fmt.Fprintf(&s, `<c r="%s"><v>%s</v></c>`, ref, xmlEscape(*v))
			default:
				fmt.Fprintf(&s, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, xmlEscape(*v))
			}
		}
		s.WriteString(`</row>`)
	}
	s.WriteString(`</sheetData></worksheet>`)
	if err := put("xl/worksheets/sheet1.xml", s.String()); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// colName: 0 -> A, 25 -> Z, 26 -> AA.
func colName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sheetName: Excel allows 31 characters and none of : \ / ? * [ ].
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" {
		s = "Report"
	}
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	return s
}
