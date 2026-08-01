package catalog

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestParseFrenchCSVNormalizesMoneyTaxAndDuration(t *testing.T) {
	content := []byte("Référence;Libellé;Type;Prix;Type de prix;TVA;Durée\nVID-01;Vidange complète;Prestation;89,90;Fixe;20%;1h30\n")
	format, rows, rejection := parseUpload("tarifs.csv", content)
	if rejection != "" || format != "csv" || len(rows) != 1 {
		t.Fatalf("parse = format %q rejection %q rows %#v", format, rejection, rows)
	}
	row := rows[0]
	if row.Issue != "" || row.Values.Kind != "service" || row.Values.AmountCents == nil ||
		*row.Values.AmountCents != 8990 || row.Values.VATBasisPoints != 2000 ||
		row.Values.DurationMinutes == nil || *row.Values.DurationMinutes != 90 {
		t.Fatalf("normalized row = %#v", row)
	}
}

func TestParseCSVRejectsMissingColumnsAndUnsafeAmounts(t *testing.T) {
	_, _, rejection := parseUpload("bad.csv", []byte("name;description\nVidange;Sans prix\n"))
	if rejection != "no_columns" {
		t.Fatalf("missing columns rejection = %q", rejection)
	}
	_, rows, rejection := parseUpload("bad.csv", []byte("name;price\nVidange;-12,50\n"))
	if rejection != "" || len(rows) != 1 || rows[0].Issue != "Prix illisible" {
		t.Fatalf("unsafe amount = rejection %q rows %#v", rejection, rows)
	}
}

func TestPriceHeaderPreservesHTBasis(t *testing.T) {
	_, rows, rejection := parseUpload("tarifs.csv", []byte("Libellé;Prix HT\nMain-d’œuvre;85\n"))
	if rejection != "" || len(rows) != 1 || rows[0].Values.TaxBasis != "excl" {
		t.Fatalf("HT header = rejection %q rows %#v", rejection, rows)
	}
}

func TestImportRejectsUnknownExplicitSemantics(t *testing.T) {
	_, rows, rejection := parseUpload(
		"tarifs.csv",
		[]byte("name;type;price;price type;tax basis\nContrôle;magie;25;peut-être;secret\n"),
	)
	if rejection != "" || len(rows) != 1 || rows[0].Issue != "Type de ligne inconnu" {
		t.Fatalf("unknown semantics = rejection %q rows %#v", rejection, rows)
	}
}

func TestParseMinimalXLSX(t *testing.T) {
	content := minimalXLSX(t)
	format, rows, rejection := parseUpload("catalogue.xlsx", content)
	if rejection != "" || format != "xlsx" || len(rows) != 1 {
		t.Fatalf("parse = format %q rejection %q rows %#v", format, rejection, rows)
	}
	if rows[0].Values.Name != "Diagnostic" || rows[0].Values.AmountCents == nil ||
		*rows[0].Values.AmountCents != 4500 {
		t.Fatalf("XLSX row = %#v", rows[0])
	}
}

func minimalXLSX(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	write := func(name, value string) {
		t.Helper()
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	write("xl/workbook.xml", `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Catalogue" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	write("xl/_rels/workbook.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	write("xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Libellé</t></is></c><c r="B1" t="inlineStr"><is><t>Prix</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>Diagnostic</t></is></c><c r="B2"><v>45</v></c></row></sheetData></worksheet>`)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
