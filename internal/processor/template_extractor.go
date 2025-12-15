// template_extractor.go - Helper functions to extract template information from AI response

package processor

import (
	"strings"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"go.mongodb.org/mongo-driver/bson"
)

// ExtractTemplateInfo analyzes AI response to determine if a template was used
// and extracts relevant information about the template selection
func ExtractTemplateInfo(accountingResponse map[string]interface{}, documentTemplates []bson.M, matchedTemplate *bson.M, reqCtx *common.RequestContext) map[string]interface{} {
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

	// Get reasoning for template matching
	reasoning, _ := aiExplanation["reasoning"].(string)

	// 🔥 PRIORITY 1: Check matchedTemplate first (most reliable source)
	if matchedTemplate != nil {
		// Get template description from matchedTemplate
		templateDesc := ""
		if desc, ok := (*matchedTemplate)["description"].(string); ok {
			templateDesc = strings.TrimSpace(desc)
		}

		if templateDesc == "" {
			// Fallback: use name if description is empty
			if name, ok := (*matchedTemplate)["name"].(string); ok {
				templateDesc = strings.TrimSpace(name)
			}
		}

		// Get template details from AI response
		templateDetails := ""
		accountSelectionLogic, _ := aiExplanation["account_selection_logic"].(map[string]interface{})
		if td, ok := accountSelectionLogic["template_details"].(string); ok {
			templateDetails = td
		}

		reqCtx.LogInfo("✅ ใช้เทมเพลต: '%s' (จาก Template Matcher)", templateDesc)
		return extractTemplateAccounts(*matchedTemplate, templateDesc, templateDetails, reqCtx)
	}

	// 🔥 PRIORITY 2: Check account_selection_logic.template_used from AI
	accountSelectionLogic, ok := aiExplanation["account_selection_logic"].(map[string]interface{})
	if ok {
		// Support both bool and string type for template_used
		templateUsed := false
		if tu, ok := accountSelectionLogic["template_used"].(bool); ok {
			templateUsed = tu
		} else if tuStr, ok := accountSelectionLogic["template_used"].(string); ok {
			templateUsed = (tuStr == "true")
		}

		if !templateUsed {
			reqCtx.LogInfo("ℹ️  AI ระบุชัดเจน: template_used = false (ไม่ใช้เทมเพลต)")
			templateDetails := ""
			if td, ok := accountSelectionLogic["template_details"].(string); ok {
				templateDetails = td
			}
			return map[string]interface{}{
				"template_used": false,
				"reason":        templateDetails,
				"note":          "AI วิเคราะห์จาก Master Data เท่านั้น ไม่ใช้เทมเพลต",
			}
		}

		// Template was definitely used (templateUsed = true) - get details
		// template_details can be string or array - handle both
		templateDetails := ""
		if td, ok := accountSelectionLogic["template_details"].(string); ok {
			templateDetails = td
		} else if tdArray, ok := accountSelectionLogic["template_details"].([]interface{}); ok {
			// If it's an array, extract account codes
			reqCtx.LogInfo("📋 AI ส่ง template_details เป็น array (ใช้เทมเพลตครบถ้วน)")
			// Convert array to a summary string if needed
			if len(tdArray) > 0 {
				templateDetails = "ใช้บัญชีจาก template ครบถ้วน"
			}
		}

		// Fallback: Try to find matching template by searching in reasoning/templateDetails
		templateDesc := ""
		var foundTemplate bson.M

		for _, template := range documentTemplates {
			if desc, ok := template["description"].(string); ok && desc != "" {
				descTrimmed := strings.TrimSpace(desc)
				if strings.Contains(reasoning, descTrimmed) || strings.Contains(templateDetails, descTrimmed) {
					templateDesc = descTrimmed
					foundTemplate = template
					break
				}
			}
		}

		if templateDesc != "" {
			reqCtx.LogInfo("📋 AI เลือกใช้เทมเพลต (หาจาก reasoning): '%s' (Confidence: 99%%)", templateDesc)
			return extractTemplateAccounts(foundTemplate, templateDesc, templateDetails, reqCtx)
		}

		// Last resort: AI says template used but we can't find it
		if templateDetails != "" {
			reqCtx.LogWarning("⚠️  AI ระบุใช้เทมเพลตแต่ไม่พบ template ที่ตรงกัน")
			return map[string]interface{}{
				"template_used":    true,
				"template_name":    templateDetails,
				"selection_reason": "ไม่พบ template ที่ตรงกัน",
				"confidence":       99,
			}
		}
	}

	// Fallback: If account_selection_logic doesn't have template_used field
	// assume no template was used (safer default)
	reqCtx.LogWarning("⚠️  ไม่พบ template_used ใน account_selection_logic - สันนิษฐานว่าไม่ใช้เทมเพลต")
	return map[string]interface{}{
		"template_used": false,
		"reason":        "ไม่พบข้อมูล template_used จาก AI - วิเคราะห์จาก Master Data",
		"note":          "AI response อาจมีรูปแบบไม่สมบูรณ์",
	}
}

// extractTemplateAccounts extracts account information from matched template
func extractTemplateAccounts(matchedTemplate bson.M, templateDesc string, selectionReason string, reqCtx *common.RequestContext) map[string]interface{} {
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
		"selection_reason": selectionReason,
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
