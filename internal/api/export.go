package api

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Bytes is a size cell: a plain number in CSV, a number shown as KB/MB/GB in XLSX.
type Bytes int64

// exportTable is one export, written as CSV (machine) or XLSX (human readable).
// Cells: string, int, int64, Bytes, bool, time.Time.
type exportTable struct {
	File  string // file name without extension
	Sheet string
	Head  []string
	Rows  [][]any
}

// sendTable answers with ?format=xlsx as a spreadsheet, anything else as CSV.
func sendTable(w http.ResponseWriter, r *http.Request, t exportTable) {
	if r.URL.Query().Get("format") == "xlsx" {
		b, err := t.xlsx()
		if err != nil {
			jerr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xlsx"`, t.File))
		w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, t.File))
	cw := csv.NewWriter(w)
	cw.Write(t.Head)
	for _, row := range t.Rows {
		rec := make([]string, len(row))
		for i, c := range row {
			rec[i] = csvCell(c)
		}
		cw.Write(rec)
	}
	cw.Flush()
}

func csvCell(c any) string {
	switch v := c.(type) {
	case string:
		return csvSafe(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case Bytes:
		return strconv.FormatInt(int64(v), 10)
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		return v.Format(time.RFC3339)
	}
	return ""
}

const (
	stDefault = iota
	stHead
	stTime
	stBytes
)

// xlsx writes a minimal single-sheet workbook (no dependency): bold frozen header row,
// auto filter, sensible column widths, local date/time cells and KB/MB/GB size cells.
func (t exportTable) xlsx() ([]byte, error) {
	var sheet bytes.Buffer
	sheet.WriteString(xml.Header)
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	n := len(t.Head)
	sheet.WriteString(fmt.Sprintf(`<dimension ref="A1:%s%d"/>`, colName(n-1), len(t.Rows)+1))
	sheet.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	widths := make([]int, n)
	for i, h := range t.Head {
		widths[i] = utf8.RuneCountInString(h) + 2
	}
	var data bytes.Buffer
	data.WriteString(`<row r="1">`)
	for i, h := range t.Head {
		data.WriteString(strCell(colName(i)+"1", h, stHead))
	}
	data.WriteString(`</row>`)
	for ri, row := range t.Rows {
		ref := strconv.Itoa(ri + 2)
		data.WriteString(`<row r="` + ref + `">`)
		for ci := 0; ci < n && ci < len(row); ci++ {
			at := colName(ci) + ref
			w := 0
			switch v := row[ci].(type) {
			case string:
				data.WriteString(strCell(at, v, stDefault))
				w = utf8.RuneCountInString(v) + 1
			case int:
				data.WriteString(numCell(at, float64(v), stDefault))
				w = len(strconv.Itoa(v)) + 1
			case int64:
				data.WriteString(numCell(at, float64(v), stDefault))
				w = len(strconv.FormatInt(v, 10)) + 1
			case Bytes:
				data.WriteString(numCell(at, float64(v), stBytes))
				w = 11
			case bool:
				data.WriteString(strCell(at, map[bool]string{true: "yes", false: "no"}[v], stDefault))
				w = 4
			case time.Time:
				data.WriteString(numCell(at, excelTime(v), stTime))
				w = 20
			}
			if w > widths[ci] {
				widths[ci] = w
			}
		}
		data.WriteString(`</row>`)
	}
	sheet.WriteString(`<cols>`)
	for i, w := range widths {
		if w > 70 {
			w = 70
		}
		sheet.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, w))
	}
	sheet.WriteString(`</cols><sheetData>`)
	sheet.Write(data.Bytes())
	sheet.WriteString(`</sheetData>`)
	sheet.WriteString(fmt.Sprintf(`<autoFilter ref="A1:%s%d"/>`, colName(n-1), len(t.Rows)+1))
	sheet.WriteString(`</worksheet>`)

	name := t.Sheet
	if name == "" {
		name = "mtmon"
	}
	var esc bytes.Buffer
	xml.EscapeText(&esc, []byte(sheetName(name)))
	files := []struct{ name, body string }{
		{"[Content_Types].xml", xml.Header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`},
		{"_rels/.rels", xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", xml.Header + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="` + esc.String() + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{"xl/styles.xml", xml.Header + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<numFmts count="2"><numFmt numFmtId="164" formatCode="yyyy\-mm\-dd\ hh:mm:ss"/><numFmt numFmtId="165" formatCode="[&gt;=1073741824]0.00,,,&quot; GB&quot;;[&gt;=1048576]0.00,,&quot; MB&quot;;0.0,&quot; KB&quot;"/></numFmts>` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDDE6F0"/></patternFill></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="4"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/><xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/><xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs>` +
			`</styleSheet>`},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func strCell(ref, s string, style int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return fmt.Sprintf(`<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, b.String())
}

func numCell(ref string, v float64, style int) string {
	return fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, strconv.FormatFloat(v, 'f', -1, 64))
}

// excelTime is the local wall-clock time as an Excel serial date.
func excelTime(t time.Time) float64 {
	l := t.Local()
	wall := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
	return float64(wall.Unix())/86400 + 25569
}

func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

// sheetName: Excel limits sheet names to 31 chars and forbids : \ / ? * [ ]
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return '-'
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	return s
}
