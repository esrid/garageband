package catalog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type parsedRow struct {
	Number int
	Raw    map[string]string
	Values ItemInput
	Issue  string
}

type fileParser interface {
	Parse([]byte) ([]map[string]string, error)
}

func parseUpload(filename string, content []byte) (string, []parsedRow, string) {
	format := uploadFormat(filename)
	var parser fileParser
	switch format {
	case "csv":
		parser = csvFileParser{}
	case "xlsx":
		parser = xlsxFileParser{}
	default:
		return "unknown", nil, "unsupported"
	}
	if len(content) == 0 {
		return format, nil, "empty"
	}
	records, err := parser.Parse(content)
	if err != nil {
		if errors.Is(err, errMissingColumns) {
			return format, nil, "no_columns"
		}
		return format, nil, "unreadable"
	}
	if len(records) == 0 {
		return format, nil, "empty"
	}
	rows := make([]parsedRow, 0, len(records))
	for index, raw := range records {
		values, issue := normalizeImportedRow(raw)
		rows = append(rows, parsedRow{
			Number: index + 2,
			Raw:    raw,
			Values: values,
			Issue:  issue,
		})
	}
	return format, rows, ""
}

// importTemplateCSV is a ready-to-fill example matching what parseUpload
// accepts, so a person never has to guess column names or number formats.
// It is exercised by TestImportTemplateParsesCleanly rather than left to
// rot silently if the parser's expectations ever drift.
func importTemplateCSV() []byte {
	var buf bytes.Buffer
	buf.WriteString("\ufeff") // BOM: Excel needs it to open UTF-8 as UTF-8.
	writer := csv.NewWriter(&buf)
	writer.Comma = ';'
	for _, row := range [][]string{
		{"Nom", "Type", "Référence", "Description", "Type de prix", "Prix", "Prix max", "TVA", "Durée", "Base taxe"},
		{"Vidange complète", "Prestation", "VID-01", "Huile et filtre inclus", "Fixe", "89,90", "", "20", "60", "TTC"},
		{"Plaquettes de frein avant", "Prestation", "FRE-01", "Remplacement des deux plaquettes", "A partir de", "120,00", "", "20", "90", "TTC"},
		{"Diagnostic électronique", "Prestation", "DIAG-01", "Lecture des codes défaut", "Fourchette", "39,00", "79,00", "20", "30", "TTC"},
		{"Forfait embrayage", "Forfait", "EMB-01", "Remplacement kit embrayage complet", "Sur devis", "", "", "20", "240", "TTC"},
	} {
		_ = writer.Write(row) // writing to a bytes.Buffer cannot fail
	}
	writer.Flush()
	return buf.Bytes()
}

func uploadFormat(filename string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".csv":
		return "csv"
	case ".xlsx":
		return "xlsx"
	}
	return "unknown"
}

var errMissingColumns = errors.New("catalog import columns are missing")

type csvFileParser struct{}

func (csvFileParser) Parse(content []byte) ([]map[string]string, error) {
	if !utf8.Valid(content) {
		return nil, errors.New("CSV is not UTF-8")
	}
	content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	var best [][]string
	for _, comma := range []rune{';', ',', '\t'} {
		reader := csv.NewReader(bytes.NewReader(content))
		reader.Comma = comma
		reader.FieldsPerRecord = -1
		reader.TrimLeadingSpace = true
		records, err := reader.ReadAll()
		if err == nil && len(records) > 0 && (best == nil || len(records[0]) > len(best[0])) {
			best = records
		}
	}
	if len(best) == 0 {
		return nil, errors.New("CSV has no records")
	}
	return tabularRows(best)
}

func tabularRows(records [][]string) ([]map[string]string, error) {
	if len(records) == 0 {
		return nil, nil
	}
	headers := make([]string, len(records[0]))
	hasName, hasPrice := false, false
	taxHint := ""
	for index, header := range records[0] {
		headers[index] = canonicalHeader(header)
		normalizedHeader := fold(header)
		if normalizedHeader == "prix ht" {
			taxHint = "excl"
		} else if normalizedHeader == "prix ttc" && taxHint == "" {
			taxHint = "incl"
		}
		hasName = hasName || headers[index] == "name"
		hasPrice = hasPrice || headers[index] == "amount" || headers[index] == "price_kind"
	}
	if !hasName || !hasPrice {
		return nil, errMissingColumns
	}
	result := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string)
		empty := true
		for index, value := range record {
			value = strings.TrimSpace(value)
			if value != "" {
				empty = false
			}
			if index < len(headers) && headers[index] != "" {
				row[headers[index]] = value
			}
		}
		if !empty {
			if _, explicit := row["tax_basis"]; !explicit && taxHint != "" {
				row["tax_basis"] = taxHint
			}
			result = append(result, row)
		}
	}
	return result, nil
}

func canonicalHeader(value string) string {
	value = fold(value)
	switch value {
	case "nom", "libelle", "designation", "prestation", "produit", "name", "label":
		return "name"
	case "type", "categorie", "category", "kind":
		return "kind"
	case "reference", "ref", "sku", "code":
		return "reference"
	case "description", "details", "detail":
		return "description"
	case "prix", "prix ttc", "prix ht", "montant", "amount", "price", "tarif":
		return "amount"
	case "prix max", "montant max", "max amount", "maximum", "price max":
		return "max_amount"
	case "type de prix", "price kind", "price type", "tarification":
		return "price_kind"
	case "ttc ht", "base taxe", "tax basis", "taxe":
		return "tax_basis"
	case "tva", "taux tva", "vat", "vat rate":
		return "vat_rate"
	case "duree", "duree minutes", "duration", "duration minutes":
		return "duration"
	case "debut", "date debut", "effective from", "valide du":
		return "effective_from"
	case "fin", "date fin", "effective to", "valide au":
		return "effective_to"
	}
	return ""
}

func normalizeImportedRow(raw map[string]string) (ItemInput, string) {
	rawKind := strings.TrimSpace(raw["kind"])
	rawPriceKind := strings.TrimSpace(raw["price_kind"])
	rawTaxBasis := strings.TrimSpace(raw["tax_basis"])
	input := ItemInput{
		Kind:           strings.TrimSpace(normalizeKind(rawKind)),
		Reference:      strings.TrimSpace(raw["reference"]),
		Name:           strings.TrimSpace(raw["name"]),
		Description:    strings.TrimSpace(raw["description"]),
		PriceKind:      strings.TrimSpace(normalizePriceKind(rawPriceKind)),
		TaxBasis:       normalizeTaxBasis(rawTaxBasis),
		VATBasisPoints: 2000,
		LocationScope:  "selected",
	}
	if rawKind == "" {
		input.Kind = "service"
	} else if input.Kind == "" {
		return input, "Type de ligne inconnu"
	}
	if input.Name == "" {
		return input, "Libellé manquant"
	}
	if len([]rune(input.Name)) > 160 {
		return input, "Libellé trop long"
	}
	if len([]rune(input.Reference)) > 80 {
		return input, "Référence trop longue"
	}
	if len([]rune(input.Description)) > 2000 {
		return input, "Description trop longue"
	}
	if rawPriceKind != "" && input.PriceKind == "" {
		return input, "Type de prix inconnu"
	}
	if input.PriceKind == "" {
		if strings.TrimSpace(raw["amount"]) == "" {
			return input, "Prix manquant"
		}
		input.PriceKind = "fixed"
	}
	if input.PriceKind != "quote" {
		amount, err := parseMoney(raw["amount"])
		if err != nil {
			return input, "Prix illisible"
		}
		input.AmountCents = &amount
	}
	if input.PriceKind == "range" {
		maximum, err := parseMoney(raw["max_amount"])
		if err != nil || input.AmountCents == nil || maximum < *input.AmountCents {
			return input, "Fourchette de prix invalide"
		}
		input.MaxAmountCents = &maximum
	}
	if rawTaxBasis != "" && !recognizedTaxBasis(rawTaxBasis) {
		return input, "Base de taxe inconnue"
	}
	if rawVAT := strings.TrimSpace(raw["vat_rate"]); rawVAT != "" {
		vat, err := parseRate(rawVAT)
		if err != nil {
			return input, "Taux de TVA illisible"
		}
		input.VATBasisPoints = vat
	}
	if rawDuration := strings.TrimSpace(raw["duration"]); rawDuration != "" {
		duration, err := parseDuration(rawDuration)
		if err != nil || (input.Kind != "service" && input.Kind != "package") {
			return input, "Durée invalide"
		}
		input.DurationMinutes = &duration
	}
	var err error
	if input.EffectiveFrom, err = parseOptionalDate(raw["effective_from"]); err != nil {
		return input, "Date de début illisible"
	}
	if input.EffectiveTo, err = parseOptionalDate(raw["effective_to"]); err != nil {
		return input, "Date de fin illisible"
	}
	if input.EffectiveFrom != nil && input.EffectiveTo != nil && input.EffectiveFrom.After(*input.EffectiveTo) {
		return input, "La date de fin précède la date de début"
	}
	return input, ""
}

func normalizeKind(value string) string {
	switch fold(value) {
	case "", "service", "prestation":
		return "service"
	case "product", "produit", "piece":
		return "product"
	case "package", "forfait":
		return "package"
	case "supplement", "option", "extra":
		return "supplement"
	case "labour rate", "labor rate", "main d oeuvre", "taux horaire", "heure":
		return "labour_rate"
	}
	return ""
}

func normalizePriceKind(value string) string {
	switch fold(value) {
	case "":
		return ""
	case "fixed", "fixe", "ferme":
		return "fixed"
	case "from", "a partir de", "des":
		return "from"
	case "range", "fourchette", "entre":
		return "range"
	case "quote", "devis", "sur devis":
		return "quote"
	}
	return ""
}

func normalizeTaxBasis(value string) string {
	value = fold(value)
	if value == "ht" || value == "excl" || value == "hors taxe" {
		return "excl"
	}
	// Preserve the safest customer-facing default unless the export says HT
	// explicitly. tabularRows carries a "prix HT" header into this field.
	return "incl"
}

func recognizedTaxBasis(value string) bool {
	switch fold(value) {
	case "ttc", "incl", "toutes taxes comprises", "ht", "excl", "hors taxe":
		return true
	}
	return false
}

func parseMoney(value string) (int64, error) {
	value = strings.TrimSpace(strings.NewReplacer("€", "", "EUR", "", "eur", "", "\u00a0", "", " ", "").Replace(value))
	if value == "" {
		return 0, errors.New("amount is empty")
	}
	comma, dot := strings.LastIndex(value, ","), strings.LastIndex(value, ".")
	decimal := byte(0)
	if comma >= 0 && dot >= 0 {
		if comma > dot {
			decimal = ','
		} else {
			decimal = '.'
		}
	} else if comma >= 0 {
		decimal = ','
	} else if dot >= 0 {
		decimal = '.'
	}
	if decimal != 0 {
		other := ","
		if decimal == ',' {
			other = "."
		}
		value = strings.ReplaceAll(value, other, "")
		value = strings.Replace(value, string(decimal), ".", 1)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid amount")
	}
	units, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || units < 0 {
		return 0, errors.New("invalid amount")
	}
	cents := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 1 {
			parts[1] += "0"
		}
		if len(parts[1]) != 2 {
			return 0, errors.New("amount must have at most two decimals")
		}
		cents, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, errors.New("invalid amount")
		}
	}
	if units > (int64(^uint(0)>>1)-cents)/100 {
		return 0, errors.New("amount is too large")
	}
	result := units*100 + cents
	if result > 2147483647 {
		return 0, errors.New("amount is too large")
	}
	return result, nil
}

func parseRate(value string) (int, error) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	cents, err := parseMoney(value)
	if err != nil || cents > 10000 {
		return 0, errors.New("invalid rate")
	}
	return int(cents), nil
}

func parseDuration(value string) (int, error) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	if strings.Contains(value, "h") {
		parts := strings.SplitN(value, "h", 2)
		hours, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}
		minutes := 0
		if parts[1] != "" {
			minutes, err = strconv.Atoi(strings.TrimSuffix(parts[1], "min"))
			if err != nil {
				return 0, err
			}
		}
		value = strconv.Itoa(hours*60 + minutes)
	} else {
		value = strings.TrimSuffix(value, "min")
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 5 || minutes > 1440 {
		return 0, errors.New("invalid duration")
	}
	return minutes, nil
}

func parseOptionalDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid date %q", value)
}

func fold(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		switch r {
		case 'à', 'â', 'ä':
			return 'a'
		case 'ç':
			return 'c'
		case 'é', 'è', 'ê', 'ë':
			return 'e'
		case 'î', 'ï':
			return 'i'
		case 'ô', 'ö':
			return 'o'
		case 'ù', 'û', 'ü':
			return 'u'
		case 'œ':
			return 'o'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return content, errors.New("content exceeds limit")
	}
	return content, nil
}
