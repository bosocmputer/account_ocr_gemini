// template_extractor.go - Helper functions to extract template information from AI response

package main

import (
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// ExtractTemplateInfo analyzes AI response to determine if a template was used
// and extracts relevant information about the template selection
func ExtractTemplateInfo(accountingResponse map[string]interface{}, documentTemplates []bson.M, reqCtx *RequestContext) map[string]interface{} {
	// Try to get AI explanation from validation
	validation, ok := accountingResponse["validation"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"template_used": false,
			"reason":        "ไม่มีข้อมูล validation จาก AI",
		}
	}

	aiExplanation, ok := validation["ai_explanation"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{
			"template_used": false,
			"reason":        "ไม่มีคำอธิบายจาก AI",
		}
	}

	reasoning, ok := aiExplanation["reasoning"].(string)
	if !ok || reasoning == "" {
		return map[string]interface{}{
			"template_used": false,
			"reason":        "ไม่มี reasoning จาก AI",
		}
	}

	// Check if template was mentioned (in Thai or English)
	templateMentioned := strings.Contains(reasoning, "เทมเพลต") ||
		strings.Contains(reasoning, "template") ||
		strings.Contains(reasoning, "Template")

	if !templateMentioned {
		reqCtx.LogInfo("ℹ️  ไม่พบการใช้เทมเพลต - AI วิเคราะห์จาก Master Data")
		return map[string]interface{}{
			"template_used": false,
			"reason":        "ไม่พบเทมเพลตที่ตรงกัน AI วิเคราะห์ตาม Master Data",
		}
	}

	// Template was used - find which one
	templateDesc := ""
	var matchedTemplate bson.M

	for _, template := range documentTemplates {
		if desc, ok := template["description"].(string); ok && desc != "" {
			if strings.Contains(reasoning, desc) {
				templateDesc = desc
				matchedTemplate = template
				break
			}
		}
	}

	if templateDesc == "" {
		// Template mentioned but couldn't identify which one
		reqCtx.LogWarning("⚠️  AI กล่าวถึงเทมเพลตแต่ไม่พบชื่อที่ตรงกัน")
		return map[string]interface{}{
			"template_used":    true,
			"template_name":    "ไม่ระบุ",
			"selection_reason": extractShortReason(reasoning),
			"confidence":       85,
		}
	}

	// Successfully identified template
	reqCtx.LogInfo("📋 AI เลือกใช้เทมเพลต: '%s' (Confidence: 99%%)", templateDesc)

	// Extract accounts used from template details
	accountsUsed := []map[string]interface{}{}

	// Try bson.A first (MongoDB array type)
	if details, ok := matchedTemplate["details"].(bson.A); ok {
		for _, detail := range details {
			if detailMap, ok := detail.(bson.M); ok {
				accountCode := ""
				accountName := ""
				if ac, ok := detailMap["accountcode"].(string); ok {
					accountCode = ac
				}
				if an, ok := detailMap["detail"].(string); ok {
					accountName = an
				}
				if accountCode != "" {
					accountsUsed = append(accountsUsed, map[string]interface{}{
						"account_code": accountCode,
						"account_name": accountName,
					})
				}
			}
		}
	} else if details, ok := matchedTemplate["details"].([]interface{}); ok {
		// Fallback to []interface{}
		for _, detail := range details {
			if detailMap, ok := detail.(map[string]interface{}); ok {
				accountCode := ""
				accountName := ""
				if ac, ok := detailMap["accountcode"].(string); ok {
					accountCode = ac
				}
				if an, ok := detailMap["detail"].(string); ok {
					accountName = an
				}
				if accountCode != "" {
					accountsUsed = append(accountsUsed, map[string]interface{}{
						"account_code": accountCode,
						"account_name": accountName,
					})
				}
			}
		}
	}

	return map[string]interface{}{
		"template_used":    true,
		"template_name":    templateDesc,
		"template_id":      matchedTemplate["_id"],
		"accounts_used":    accountsUsed,
		"selection_reason": extractShortReason(reasoning),
		"confidence":       99,
		"note":             "AI วิเคราะห์แล้วพบว่าใบเสร็จตรงกับเทมเพลตที่กำหนดไว้",
	}
}

// extractShortReason extracts a short summary from AI reasoning
// Limits to first 200 characters to keep response concise
func extractShortReason(reasoning string) string {
	// Convert to runes to handle Thai characters properly
	runes := []rune(reasoning)

	// Find template mention and extract surrounding context
	templateIdx := strings.Index(reasoning, "เทมเพลต")
	if templateIdx == -1 {
		templateIdx = strings.Index(reasoning, "template")
	}

	if templateIdx != -1 {
		// Convert byte index to rune index for proper Thai character handling
		byteToRune := 0
		for i, r := range runes {
			if byteToRune >= templateIdx {
				templateIdx = i
				break
			}
			byteToRune += len(string(r))
		}

		// Extract context around template mention (in runes)
		start := templateIdx - 30
		if start < 0 {
			start = 0
		}
		end := templateIdx + 100
		if end > len(runes) {
			end = len(runes)
		}

		excerpt := string(runes[start:end])
		if start > 0 {
			excerpt = "..." + excerpt
		}
		if end < len(runes) {
			excerpt = excerpt + "..."
		}
		return excerpt
	}

	// Fallback: return first 150 runes (not bytes)
	if len(runes) > 150 {
		return string(runes[:150]) + "..."
	}
	return reasoning
}
