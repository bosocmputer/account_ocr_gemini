// vendor_matcher.go - Fuzzy matching for vendor/customer names
package processor

import (
	"math"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// VendorMatchResult represents the result of vendor matching
type VendorMatchResult struct {
	Found      bool    `json:"found"`
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Similarity float64 `json:"similarity"`
	Method     string  `json:"method"` // exact, fuzzy, tax_id, not_found
}

// MatchVendor finds the best matching vendor from master data
// Uses fuzzy matching with Thai text normalization
func MatchVendor(vendorNameFromOCR string, creditors []bson.M, taxIDFromOCR string) VendorMatchResult {
	if vendorNameFromOCR == "" && taxIDFromOCR == "" {
		return VendorMatchResult{Found: false, Method: "not_found"}
	}

	// Try Tax ID matching first (100% reliable)
	if taxIDFromOCR != "" {
		taxIDNormalized := normalizeTaxID(taxIDFromOCR)
		for _, creditor := range creditors {
			creditorTaxID, _ := creditor["taxid"].(string)
			if creditorTaxID != "" && normalizeTaxID(creditorTaxID) == taxIDNormalized {
				code, _ := creditor["code"].(string)
				name := extractNameFromCreditor(creditor)
				return VendorMatchResult{
					Found:      true,
					Code:       code,
					Name:       name,
					Similarity: 100.0,
					Method:     "tax_id",
				}
			}
		}
	}

	// Fuzzy name matching
	if vendorNameFromOCR == "" {
		return VendorMatchResult{Found: false, Method: "not_found"}
	}

	normalizedOCR := normalizeVendorName(vendorNameFromOCR)
	if normalizedOCR == "" {
		return VendorMatchResult{Found: false, Method: "not_found"}
	}

	bestMatch := VendorMatchResult{Found: false, Similarity: 0.0, Method: "not_found"}

	for _, creditor := range creditors {
		creditorName := extractNameFromCreditor(creditor)
		if creditorName == "" {
			continue
		}

		normalizedMaster := normalizeVendorName(creditorName)
		if normalizedMaster == "" {
			continue
		}

		// Calculate similarity
		similarity := calculateNameSimilarity(normalizedOCR, normalizedMaster)

		// Update best match
		if similarity > bestMatch.Similarity {
			code, _ := creditor["code"].(string)
			bestMatch = VendorMatchResult{
				Found:      true,
				Code:       code,
				Name:       creditorName, // Use original name from Master
				Similarity: similarity,
				Method:     "fuzzy",
			}
		}

		// If exact match found, stop searching
		if similarity >= 99.0 {
			bestMatch.Method = "exact"
			break
		}
	}

	// Only return if similarity >= 70%
	if bestMatch.Similarity < 70.0 {
		return VendorMatchResult{Found: false, Method: "not_found"}
	}

	return bestMatch
}

// normalizeVendorName normalizes Thai company names for matching
func normalizeVendorName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Remove common prefixes/suffixes
	prefixes := []string{
		"บริษัท", "บจก.", "บมจ.", "บจำกัด", "บมหาชน",
		"ห้างหุ้นส่วนจำกัด", "หจก.", "ห.จำกัด",
		"company", "co.", "ltd.", "limited", "corp.", "corporation",
	}
	for _, prefix := range prefixes {
		name = strings.Replace(name, strings.ToLower(prefix), "", -1)
	}

	// Remove common suffixes
	suffixes := []string{
		"จำกัด", "มหาชน", "(มหาชน)", "จํากัด",
		"สำนักงานใหญ่", "(สำนักงานใหญ่)", "head office",
		"limited", "ltd", "corp", "inc",
	}
	for _, suffix := range suffixes {
		name = strings.Replace(name, strings.ToLower(suffix), "", -1)
	}

	// Normalize Thai special characters
	// Handle duplicated consonants: ลล์ → ล, ล์ → ล
	name = regexp.MustCompile(`ลล์|ล์`).ReplaceAllString(name, "ล")
	name = regexp.MustCompile(`รร์|ร์`).ReplaceAllString(name, "ร")
	name = regexp.MustCompile(`นน์|น์`).ReplaceAllString(name, "น")

	// Handle duplicated tone marks: ่่ → ่, ้้ → ้
	name = regexp.MustCompile(`่+`).ReplaceAllString(name, "่")
	name = regexp.MustCompile(`้+`).ReplaceAllString(name, "้")
	name = regexp.MustCompile(`๊+`).ReplaceAllString(name, "๊")
	name = regexp.MustCompile(`๋+`).ReplaceAllString(name, "๋")

	// Normalize connectors: และ, &, แอนด์ → and
	connectors := map[string]string{
		"และ":   "and",
		"&":     "and",
		"แอนด์": "and",
	}
	for old, new := range connectors {
		name = strings.Replace(name, old, new, -1)
	}

	// Remove extra spaces and special characters
	name = regexp.MustCompile(`[^\p{L}\p{N}]+`).ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// Remove multiple spaces
	name = regexp.MustCompile(`\s+`).ReplaceAllString(name, " ")

	return name
}

// normalizeTaxID removes dashes and spaces from Tax ID
func normalizeTaxID(taxID string) string {
	taxID = strings.ReplaceAll(taxID, "-", "")
	taxID = strings.ReplaceAll(taxID, " ", "")
	return strings.TrimSpace(taxID)
}

// extractNameFromCreditor extracts name from creditor document
func extractNameFromCreditor(creditor bson.M) string {
	namesField, exists := creditor["names"]
	if !exists {
		return ""
	}

	var names []interface{}
	if n, ok := namesField.([]interface{}); ok {
		names = n
	} else if n, ok := namesField.(bson.A); ok {
		names = []interface{}(n)
	} else {
		return ""
	}

	if len(names) == 0 {
		return ""
	}

	// Try Thai name first
	for _, nameInterface := range names {
		nameMap, ok := nameInterface.(bson.M)
		if !ok {
			continue
		}
		code, _ := nameMap["code"].(string)
		isDelete, _ := nameMap["isdelete"].(bool)
		name, _ := nameMap["name"].(string)

		if code == "th" && !isDelete && name != "" {
			return name
		}
	}

	// Fallback to first non-deleted name
	for _, nameInterface := range names {
		nameMap, ok := nameInterface.(bson.M)
		if !ok {
			continue
		}
		isDelete, _ := nameMap["isdelete"].(bool)
		name, _ := nameMap["name"].(string)

		if !isDelete && name != "" {
			return name
		}
	}

	return ""
}

// calculateNameSimilarity calculates similarity between two normalized names
// Uses existing Levenshtein distance function from template_matcher.go
func calculateNameSimilarity(name1, name2 string) float64 {
	// If identical, return 100%
	if name1 == name2 {
		return 100.0
	}

	// Calculate Levenshtein distance (function from template_matcher.go)
	distance := levenshteinDistance(name1, name2)
	maxLen := float64(maxInt(len(name1), len(name2)))

	if maxLen == 0 {
		return 0.0
	}

	similarity := (1.0 - (float64(distance) / maxLen)) * 100.0
	return math.Max(0, similarity)
}

// maxInt returns the maximum of two integers
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
