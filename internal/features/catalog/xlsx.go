package catalog

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
)

type xlsxFileParser struct{}

func (xlsxFileParser) Parse(content []byte) ([]map[string]string, error) {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		files[path.Clean(file.Name)] = file
	}
	shared, err := xlsxSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}
	sheetName, err := firstWorksheet(files)
	if err != nil {
		return nil, err
	}
	rows, err := xlsxRows(files[sheetName], shared)
	if err != nil {
		return nil, err
	}
	return tabularRows(rows)
}

func firstWorksheet(files map[string]*zip.File) (string, error) {
	workbook, ok := files["xl/workbook.xml"]
	if !ok {
		return "", errors.New("XLSX workbook is missing")
	}
	data, err := readZipFile(workbook, 2<<20)
	if err != nil {
		return "", err
	}
	var book struct {
		Sheets []struct {
			RelID string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(data, &book); err != nil || len(book.Sheets) == 0 {
		return "", errors.New("XLSX has no worksheet")
	}
	relsFile, ok := files["xl/_rels/workbook.xml.rels"]
	if !ok {
		return "", errors.New("XLSX workbook relationships are missing")
	}
	relsData, err := readZipFile(relsFile, 2<<20)
	if err != nil {
		return "", err
	}
	var rels struct {
		Relationships []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(relsData, &rels); err != nil {
		return "", err
	}
	for _, relationship := range rels.Relationships {
		if relationship.ID == book.Sheets[0].RelID {
			target := strings.TrimPrefix(relationship.Target, "/")
			if !strings.HasPrefix(target, "xl/") {
				target = path.Join("xl", target)
			}
			if _, exists := files[path.Clean(target)]; !exists {
				return "", errors.New("XLSX worksheet is missing")
			}
			return path.Clean(target), nil
		}
	}
	// Some producers omit namespace information that encoding/xml needs for the
	// relationship attribute. Sheet 1 is the safe interoperability fallback.
	if _, ok := files["xl/worksheets/sheet1.xml"]; ok {
		return "xl/worksheets/sheet1.xml", nil
	}
	return "", errors.New("XLSX first worksheet cannot be resolved")
}

func xlsxSharedStrings(file *zip.File) ([]string, error) {
	if file == nil {
		return nil, nil
	}
	data, err := readZipFile(file, 16<<20)
	if err != nil {
		return nil, err
	}
	var table struct {
		Items []struct {
			Text string `xml:"t"`
			Runs []struct {
				Text string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &table); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(table.Items))
	for _, item := range table.Items {
		var value strings.Builder
		value.WriteString(item.Text)
		for _, run := range item.Runs {
			value.WriteString(run.Text)
		}
		result = append(result, value.String())
	}
	return result, nil
}

func xlsxRows(file *zip.File, shared []string) ([][]string, error) {
	if file == nil {
		return nil, errors.New("XLSX worksheet is missing")
	}
	data, err := readZipFile(file, 32<<20)
	if err != nil {
		return nil, err
	}
	var worksheet struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(data, &worksheet); err != nil {
		return nil, err
	}
	result := make([][]string, 0, len(worksheet.Rows))
	for _, sourceRow := range worksheet.Rows {
		row := make([]string, 0, len(sourceRow.Cells))
		for sequential, cell := range sourceRow.Cells {
			column := sequential
			if cell.Ref != "" {
				column = xlsxColumn(cell.Ref)
			}
			for len(row) <= column {
				row = append(row, "")
			}
			value := cell.Value
			switch cell.Type {
			case "s":
				index, parseErr := strconv.Atoi(value)
				if parseErr != nil || index < 0 || index >= len(shared) {
					return nil, errors.New("XLSX shared string is invalid")
				}
				value = shared[index]
			case "inlineStr":
				value = cell.Inline
			}
			row[column] = value
		}
		result = append(result, row)
	}
	return result, nil
}

func xlsxColumn(reference string) int {
	column := 0
	for _, char := range reference {
		if char < 'A' || char > 'Z' {
			break
		}
		column = column*26 + int(char-'A'+1)
	}
	if column == 0 {
		return 0
	}
	return column - 1
}

func readZipFile(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("XLSX part exceeds %d bytes", limit)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	data, readErr := readAllLimited(reader, limit)
	closeErr := reader.Close()
	return data, errors.Join(readErr, closeErr)
}
