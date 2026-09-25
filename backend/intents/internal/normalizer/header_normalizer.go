package normalizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
)

// NormalizationResult is what the normalizer returns.
// It carries the normalized JSON ready for json.Unmarshal into ParsedIncomingIntent,
// plus provenance metadata that goes into the NIR.
type NormalizationResult struct {
	NormalizedJSON  []byte            // ready to Unmarshal into ParsedIncomingIntent
	FieldProvenance []FieldProvenance // one entry per field, for NIR
	UnmappedFields  map[string]any    // fields that had no mapping — never dropped
	Warnings        []string
	WasNormalized   bool // false = payload was already in canonical form
}

// FieldProvenance records how each field was resolved — for NIR TransformApplied.
type FieldProvenance struct {
	CanonicalPath   string
	SourceKey       string
	MatchMethod     string // "exact" | "lowercase_trim" | "synonym" | "fuzzy" | "inferred"
	RawValue        string
	NormalizedValue string
	Confidence      float64
	Transform       string // "NONE" | "AMOUNT_DECIMAL" | "CURRENCY_ISO" | "TIMESTAMP_UTC" | etc.
	Warning         string
}

// Normalize is the entry point. It takes raw decrypted JSON bytes and returns
// a NormalizationResult. It is called from intent_service.go right after decrypt.
//
// If the JSON already has canonical keys (i.e. it came from a well-formed client),
// it passes through with WasNormalized=false and zero overhead.
func Normalize(rawJSON []byte, tenantSynonyms map[string]string) (*NormalizationResult, error) {
	// Parse raw JSON into a flat map first
	// UseNumber keeps numeric literals (e.g. an amount sent as 1.005) as
	// their exact text instead of decoding them through float64 (D10).
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(rawJSON))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("normalizer: invalid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("normalizer: invalid JSON: trailing data after top-level object")
	}

	// If already canonical, we still run ensureDefaults to catch missing required fields
	var warnings []string
	if isAlreadyCanonical(raw) {
		ensureDefaults(raw, &warnings)

		// If defaults were applied, we return the updated JSON with WasNormalized=true
		if len(warnings) > 0 {
			normalizedBytes, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("normalizer: marshal failed: %w", err)
			}
			return &NormalizationResult{
				NormalizedJSON:  normalizedBytes,
				FieldProvenance: nil, // bypass provenance for already canonical
				UnmappedFields:  nil,
				Warnings:        warnings,
				WasNormalized:   true,
			}, nil
		}

		return &NormalizationResult{
			NormalizedJSON: rawJSON,
			WasNormalized:  false,
		}, nil
	}

	// Build merged synonym map: global dict + tenant-specific overrides
	merged := make(map[string]string, len(synonymDict)+len(tenantSynonyms))
	for k, v := range synonymDict {
		merged[k] = v
	}
	for k, v := range tenantSynonyms {
		merged[strings.ToLower(strings.TrimSpace(k))] = v
	}

	canonical := make(map[string]any) // will be serialized as normalized JSON
	unmapped := make(map[string]any)
	var provenance []FieldProvenance

	for rawKey, rawVal := range raw {
		prov, canonicalPath, normalizedVal, matched := resolveField(rawKey, rawVal, merged)

		if !matched {
			unmapped[rawKey] = rawVal
			warnings = append(warnings, fmt.Sprintf("unmapped field %q — preserved in unmapped_json", rawKey))
			continue
		}

		// Set into nested canonical map using dot-path
		setNestedValue(canonical, canonicalPath, normalizedVal)
		provenance = append(provenance, prov)
		if prov.Warning != "" {
			warnings = append(warnings, prov.Warning)
		}
	}

	// 10.3 — Row-level normalization: ensure required defaults
	ensureDefaults(canonical, &warnings)

	normalizedBytes, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("normalizer: marshal failed: %w", err)
	}

	return &NormalizationResult{
		NormalizedJSON:  normalizedBytes,
		FieldProvenance: provenance,
		UnmappedFields:  unmapped,
		Warnings:        warnings,
		WasNormalized:   true,
	}, nil
}

// resolveField attempts to map a raw key to a canonical path using the 4-step chain.
func resolveField(rawKey string, rawVal any, synonyms map[string]string) (FieldProvenance, string, any, bool) {
	// If value is already a map (nested object), we don't want to stringify it yet.
	// We only stringify for leaf nodes during matching/normalization.

	prov := FieldProvenance{
		SourceKey: rawKey,
	}

	// JSON Pass-through: If the value is already a nested map and the key is a known
	// canonical root (like "amount" or "beneficiary"), we preserve it as a root mapping.
	// This prevents collisions with flat synonyms like "amount" -> "amount.value".
	if _, isMap := rawVal.(map[string]any); isMap {
		if canonicalTopLevelKeys[rawKey] {
			prov.MatchMethod = "canonical_root"
			prov.CanonicalPath = rawKey
			prov.Confidence = 1.0
			normalized, transform := applyTypeNormalization(rawKey, rawVal)
			prov.NormalizedValue = fmt.Sprintf("%v", normalized)
			prov.Transform = transform
			return prov, rawKey, normalized, true
		}
	}

	// Step 1 — Exact match against synonym dict
	if canonical, ok := synonyms[rawKey]; ok {
		prov.MatchMethod = "exact"
		prov.CanonicalPath = canonical
		prov.Confidence = 1.0
		normalized, transform := applyTypeNormalization(canonical, rawVal)
		prov.NormalizedValue = fmt.Sprintf("%v", normalized)
		prov.Transform = transform
		return prov, canonical, normalized, true
	}

	// Step 2 — Lowercase + trim
	lowKey := strings.ToLower(strings.TrimSpace(rawKey))
	if canonical, ok := synonyms[lowKey]; ok {
		prov.MatchMethod = "lowercase_trim"
		prov.CanonicalPath = canonical
		prov.Confidence = 0.98
		normalized, transform := applyTypeNormalization(canonical, rawVal)
		prov.NormalizedValue = fmt.Sprintf("%v", normalized)
		prov.Transform = transform
		return prov, canonical, normalized, true
	}

	// Step 3 — Synonym dictionary (already covered by steps 1+2 since dict is lowercase)
	// Remaining: try stripping underscores and spaces
	stripped := strings.ReplaceAll(strings.ReplaceAll(lowKey, "_", ""), " ", "")
	for dictKey, canonical := range synonyms {
		dictStripped := strings.ReplaceAll(strings.ReplaceAll(dictKey, "_", ""), " ", "")
		if stripped == dictStripped {
			prov.MatchMethod = "synonym"
			prov.CanonicalPath = canonical
			prov.Confidence = 0.92
			normalized, transform := applyTypeNormalization(canonical, rawVal)
			prov.NormalizedValue = fmt.Sprintf("%v", normalized)
			prov.Transform = transform
			return prov, canonical, normalized, true
		}
	}

	// Step 4 — Fuzzy similarity (Jaro-Winkler style, simplified)
	// Only perform fuzzy match for keys longer than 3 characters to avoid collisions for short IDs like VPA/UPI
	if len(lowKey) > 3 {
		bestMatch, bestScore := fuzzyMatch(lowKey, synonyms)
		if bestScore >= 0.88 {
			prov.MatchMethod = "fuzzy"
			prov.CanonicalPath = bestMatch
			prov.Confidence = bestScore * 0.85 // reduce confidence for fuzzy
			prov.Warning = fmt.Sprintf("fuzzy match %q → %q score=%.2f", rawKey, bestMatch, bestScore)
			normalized, transform := applyTypeNormalization(bestMatch, rawVal)
			prov.NormalizedValue = fmt.Sprintf("%v", normalized)
			prov.Transform = transform
			return prov, bestMatch, normalized, true
		}
	}

	return prov, "", rawVal, false
}

// applyTypeNormalization implements 10.2 — type normalization per canonical path.
func applyTypeNormalization(canonicalPath string, rawVal any) (any, string) {
	// If rawVal is not a string (e.g. nested map), and we are mapping to a root object key,
	// just pass it through.
	if _, isMap := rawVal.(map[string]any); isMap {
		return rawVal, "NONE"
	}

	rawStr := fmt.Sprintf("%v", rawVal)

	switch canonicalPath {

	case "amount.value":
		normalized, err := normalizeAmount(rawStr)
		if err != nil {
			return rawStr, "AMOUNT_PARSE_FAILED"
		}
		return normalized, "AMOUNT_DECIMAL"

	case "amount.currency":
		return normalizeCurrency(rawStr), "CURRENCY_ISO"

	case "intended_execution_at":
		t, err := normalizeTimestamp(rawStr)
		if err != nil {
			return rawStr, "TIMESTAMP_PARSE_FAILED"
		}
		return t.UTC().Format(time.RFC3339), "TIMESTAMP_UTC"

	case "beneficiary.instrument.kind":
		return normalizeRail(rawStr), "RAIL_ENUM"

	case "provider_hint":
		return strings.ToLower(strings.TrimSpace(rawStr)), "LOWERCASE"

	default:
		return strings.TrimSpace(rawStr), "TRIM"
	}
}

// normalizeAmount handles 10.3 amount variants:
// "1,000.00" / "₹1,000" / "(1000)" / "1,00,000.00" / "1000" / "Rs. 500"
//
// Money path (D10, L3): the value is parsed as an exact decimal from the
// string — never through float64 — and is never rounded. A value with at most
// two fractional digits is emitted with exactly two ("100.5" → "100.50"). A
// value with more precision (e.g. "1.005") is emitted exactly as parsed so the
// semantic validator's exact sub-paise check rejects it instead of it being
// silently rounded to a different amount.
func normalizeAmount(raw string) (string, error) {
	s := strings.TrimSpace(raw)

	// Negative parentheses: (1000) → -1000
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[1 : len(s)-1])
		negative = true
	}

	// Drop a leading currency word whose '.' would otherwise survive the
	// character filter below ("Rs. 500" must not become ".500").
	lower := strings.ToLower(s)
	for _, prefix := range []string{"rs.", "rs", "inr", "₹"} {
		if strings.HasPrefix(lower, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}

	// Strip currency symbols and whitespace
	s = strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || r == '.' || r == '-' || r == ',' {
			return r
		}
		return -1
	}, s)

	// Remove Indian/standard comma grouping
	s = strings.ReplaceAll(s, ",", "")

	d, err := decimal.NewFromString(s)
	if err != nil || s == "" {
		return "", fmt.Errorf("cannot parse amount %q", raw)
	}

	if negative {
		d = d.Neg()
	}

	if d.Equal(d.Truncate(2)) {
		return d.StringFixed(2), nil
	}
	// Sub-paise precision: keep it exact; the validator rejects it.
	return d.String(), nil
}

// normalizeCurrency maps common variants to ISO 4217.
func normalizeCurrency(raw string) string {
	m := map[string]string{
		"inr": "INR", "rupee": "INR", "rupees": "INR", "rs": "INR", "rs.": "INR",
		"usd": "USD", "dollar": "USD", "dollars": "USD",
		"eur": "EUR", "euro": "EUR",
		"gbp": "GBP", "pound": "GBP",
		"aed": "AED", "sgd": "SGD",
	}
	key := strings.ToLower(strings.TrimSpace(raw))
	if iso, ok := m[key]; ok {
		return iso
	}
	return strings.ToUpper(strings.TrimSpace(raw))
}

// normalizeTimestamp handles mixed date formats from 10.3.
var dateFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z", // ← ADD: explicit Z suffix without timezone offset
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"02/01/2006",
	"01/02/2006",
	"2006-01-02",
	"02-01-2006",
	"Jan 2, 2006",
	"2 Jan 2006",
	"02 Jan 2006",
	"January 2, 2006",
	"2 January 2006",  // ← ADD: "20 May 2026" variant
	"02 January 2006", // ← ADD
}

func normalizeTimestamp(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp %q", raw)
}

// normalizeRail maps payment mode variants to Zord rail enum.
func normalizeRail(raw string) string {
	m := map[string]string{
		"neft": "NEFT", "imps": "IMPS", "rtgs": "RTGS",
		"upi": "UPI", "upi/imps": "UPI", "bank": "NEFT",
		"wallet": "WALLET", "card": "CARD",
	}
	key := strings.ToLower(strings.TrimSpace(raw))
	if v, ok := m[key]; ok {
		return v
	}
	return strings.ToUpper(strings.TrimSpace(raw))
}

// fuzzyMatch returns the best canonical path and similarity score for a key.
func fuzzyMatch(input string, synonyms map[string]string) (string, float64) {
	best := ""
	bestScore := 0.0
	for dictKey, canonical := range synonyms {
		score := jaroWinkler(input, dictKey)
		if score > bestScore {
			bestScore = score
			best = canonical
		}
	}
	return best, bestScore
}

// jaroWinkler computes a simplified Jaro-Winkler similarity.
func jaroWinkler(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	l1, l2 := len(s1), len(s2)
	if l1 == 0 || l2 == 0 {
		return 0.0
	}
	matchDist := max(l1, l2)/2 - 1
	if matchDist < 0 {
		matchDist = 0
	}
	s1m := make([]bool, l1)
	s2m := make([]bool, l2)
	matches := 0
	transpositions := 0
	for i := 0; i < l1; i++ {
		start := max(0, i-matchDist)
		end := min(l2-1, i+matchDist)
		for j := start; j <= end; j++ {
			if s2m[j] || s1[i] != s2[j] {
				continue
			}
			s1m[i] = true
			s2m[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0.0
	}
	k := 0
	for i := 0; i < l1; i++ {
		if !s1m[i] {
			continue
		}
		for !s2m[k] {
			k++
		}
		if s1[i] != s2[k] {
			transpositions++
		}
		k++
	}
	jaro := (float64(matches)/float64(l1) +
		float64(matches)/float64(l2) +
		float64(matches-transpositions/2)/float64(matches)) / 3.0
	prefix := 0
	for i := 0; i < min(4, min(l1, l2)); i++ {
		if s1[i] == s2[i] {
			prefix++
		} else {
			break
		}
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

// setNestedValue sets a value at a dot-separated path in a map.
// "amount.value" → map["amount"]["value"] = val
func setNestedValue(m map[string]any, path string, val any) {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		m[path] = val
		return
	}
	if _, ok := m[parts[0]]; !ok {
		m[parts[0]] = make(map[string]any)
	}
	if sub, ok := m[parts[0]].(map[string]any); ok {
		setNestedValue(sub, parts[1], val)
	}
}

// isAlreadyCanonical returns true if the JSON already uses Zord canonical top-level keys.
// This is a fast path — avoids normalization overhead for well-formed clients.
var canonicalTopLevelKeys = map[string]bool{
	"intent_type": true, "amount": true, "beneficiary": true,
	"idempotency_key": true, "account_number": true,
	"client_payout_ref": true, "client_batch_ref": true,
	"intended_execution_at": true,
	"purpose_code":          true, "remitter": true, "constraints": true,
	"schema_version": true, "governance_hash": true,
	"invoice_number": true, "invoice_date": true, "vendor_gstin": true,
	"pan_number": true, "tan_of_deductor": true, "gst_type": true,
	"igst_amount": true, "cgst_amount": true, "sgst_amount": true,
	"gst_rate": true, "taxable_value": true, "tds_section": true,
	"tds_rate": true, "tds_amount": true, "net_payable": true,
	"hsn_sac_code": true, "po_number": true, "product_id": true,
	"product_desc": true, "payout_purpose": true, "mcc_code": true,
	"vendor_type": true, "kyc_status": true, "kyc_policy_class": true,
	"bank_verified": true, "cin_number": true, "msme_number": true,
	"gateway_name": true, "fund_account_id": true, "contact_id": true,
	"kyc_verified_at": true, "source": true, "source_system": true,
	"source_row_ref": true,
}

func isAlreadyCanonical(raw map[string]any) bool {
	for key := range raw {
		if !canonicalTopLevelKeys[key] {
			return false // has at least one non-canonical key — normalize
		}
	}
	return true
}

// ensureDefaults applies 10.3 row-level defaults.
func ensureDefaults(canonical map[string]any, warnings *[]string) {
	// ── Hardcode schema_version to v1 ─────────────────────────────────────────
	canonical["schema_version"] = "v1"

	// ── Default intent_type to PAYOUT ────────────────────────────────────────
	// ERP/Tally/SAP CSVs never contain an intent_type column. Without this
	// default the field arrives empty → ApplyPolicy sets SemanticValid=false
	// with MissingFields=["intent_type"] but SemanticErrors=[] (confusing log).
	if v, _ := canonical["intent_type"].(string); v == "" {
		canonical["intent_type"] = "PAYOUT"
		*warnings = append(*warnings, "intent_type defaulted to PAYOUT")
	}

	// ── Default amount.currency to INR ────────────────────────────────────────
	// Tally/SAP CSVs rarely carry a currency column. If amount.value is present
	// but currency is absent, default to INR (the system's base currency).
	if amtMap, ok := canonical["amount"].(map[string]any); ok {
		if amtMap["currency"] == nil || amtMap["currency"] == "" {
			amtMap["currency"] = "INR"
			*warnings = append(*warnings, "amount.currency defaulted to INR")
		}
	}

	// ── Normalize beneficiary.instrument.kind ─────────────────────────────────
	if benMap, ok := canonical["beneficiary"].(map[string]any); ok {
		if instMap, ok := benMap["instrument"].(map[string]any); ok {
			// Normalize the kind enum if present (e.g. "neft" → "NEFT")
			if kind, ok := instMap["kind"].(string); ok && kind != "" {
				instMap["kind"] = normalizeRail(kind)
			}

			// If kind is still missing but IFSC or VPA is present, infer the rail
			kindVal, _ := instMap["kind"].(string)
			if kindVal == "" {
				ifsc, _ := instMap["ifsc"].(string)
				vpa, _ := instMap["vpa"].(string)
				if vpa != "" {
					instMap["kind"] = "UPI"
					*warnings = append(*warnings, "instrument.kind inferred as UPI (vpa present)")
				} else if ifsc != "" {
					instMap["kind"] = "NEFT"
					*warnings = append(*warnings, "instrument.kind inferred as NEFT (ifsc present)")
				}
			}
		}
	}
}

var nonDigitOrDot = regexp.MustCompile(`[^\d.]`)

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
