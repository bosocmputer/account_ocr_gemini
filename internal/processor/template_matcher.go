// template_matcher.go - Smart Template Matching with AI
//
// ฟังก์ชัน: วิเคราะห์ raw_document_text และจับคู่กับ template ที่เหมาะสม
// ใช้ Gemini AI เพื่อเข้าใจบริบทและจับคู่อัจฉริยะ

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/ratelimit"
	"github.com/google/generative-ai-go/genai"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/api/option"
)

// TemplateMatchResult เก็บผลลัพธ์การจับคู่ template
type TemplateMatchResult struct {
	Template        bson.M  // Template ที่ตรงที่สุด
	Confidence      float64 // ความมั่นใจ 0-100
	MatchedKeywords []string
	Description     string
	TemplateID      interface{}
	Reason          string             // เหตุผลที่เลือก template นี้
	TokenUsage      *common.TokenUsage // Token usage from AI call (Phase 2)
}

// aiTemplateMatchResult represents AI's template matching result (internal)
type aiTemplateMatchResult struct {
	MatchedTemplate       string `json:"matched_template"`
	Confidence            int    `json:"confidence"`
	Reasoning             string `json:"reasoning"`
	CompanyNameInTemplate string `json:"company_name_in_template"` // ชื่อบริษัทที่ระบุใน template (ถ้ามี)
	CompanyLocationInDoc  string `json:"company_location_in_doc"`  // ตำแหน่งที่พบชื่อบริษัทในเอกสาร: "document_header", "received_from", "customer_name", "not_found"
	IsCompanyIssuer       bool   `json:"is_company_issuer"`        // true = บริษัทเป็นผู้ออกเอกสาร, false = บริษัทเป็นลูกค้า/ผู้จ่าย
}

// AnalyzeTemplateMatch วิเคราะห์ raw_document_text และหา template ที่เหมาะสม
//
// NEW Algorithm (AI-Driven):
// 1. ส่ง raw_document_text + template descriptions ไปให้ Gemini AI
// 2. AI จะวิเคราะห์และเลือก template ที่เหมาะสมที่สุด
// 3. AI ให้ confidence score และเหตุผล
// 4. Return template ที่ AI เลือก
func AnalyzeTemplateMatch(
	rawDocumentText string,
	templates []bson.M,
	reqCtx *common.RequestContext,
) TemplateMatchResult {
	if len(templates) == 0 {
		return TemplateMatchResult{
			Confidence: 0,
			Reason:     "ไม่มี template ในระบบ",
		}
	}

	// Extract template descriptions for AI
	templateDescriptions := make([]string, 0, len(templates))
	templateMap := make(map[string]bson.M) // Map description -> template

	for _, template := range templates {
		description, ok := template["description"].(string)
		promptDescription, promptOk := template["promptdescription"].(string)

		// Build combined description for AI matching
		var combinedDesc string
		if ok && description != "" {
			combinedDesc = description
			// Add promptdescription if available (gives more context for AI)
			if promptOk && promptDescription != "" {
				combinedDesc = description + " | " + promptDescription
			}
		} else if promptOk && promptDescription != "" {
			// Fallback: use only promptdescription if description is empty
			combinedDesc = promptDescription
		}

		if combinedDesc != "" {
			templateDescriptions = append(templateDescriptions, combinedDesc)
			// Map with original description as key for backward compatibility
			if ok && description != "" {
				templateMap[description] = template
			}
			// Also map with combined description for better matching
			templateMap[combinedDesc] = template
		}
	}

	if len(templateDescriptions) == 0 {
		return TemplateMatchResult{
			Confidence: 0,
			Reason:     "ไม่มี template description/promptdescription ในระบบ",
		}
	}

	reqCtx.LogInfo("🤖 AI Template Matching: %d templates", len(templateDescriptions))

	// Call Gemini AI for intelligent template matching
	aiResult, tokenUsage, err := callGeminiForTemplateMatch(rawDocumentText, templateDescriptions, reqCtx)
	if err != nil {
		reqCtx.LogInfo("⚠️  AI Template Matching failed: %v", err)
		// Fallback: return no match
		return TemplateMatchResult{
			Confidence: 0,
			Reason:     fmt.Sprintf("AI matching error: %v", err),
		}
	}

	// Log token usage
	if tokenUsage != nil {
		reqCtx.LogInfo("🪙 Template Matching Tokens: %d input + %d output = %d total",
			tokenUsage.InputTokens, tokenUsage.OutputTokens, tokenUsage.TotalTokens)
	}

	// 🚨 CRITICAL VALIDATION: Check company location
	// If template specifies a company name, it MUST be the document issuer, not customer/payer
	if aiResult.CompanyNameInTemplate != "" {
		reqCtx.LogInfo("🔍 Validating company location: '%s' found in '%s'",
			aiResult.CompanyNameInTemplate, aiResult.CompanyLocationInDoc)

		// Validate location
		validLocations := []string{"document_header", "issuer", "from"}
		invalidLocations := []string{"received_from", "customer_name", "customer", "bill_to", "payer", "buyer"}

		isValid := false
		for _, validLoc := range validLocations {
			if strings.Contains(strings.ToLower(aiResult.CompanyLocationInDoc), validLoc) {
				isValid = true
				break
			}
		}

		// Check if in invalid location
		for _, invalidLoc := range invalidLocations {
			if strings.Contains(strings.ToLower(aiResult.CompanyLocationInDoc), invalidLoc) {
				reqCtx.LogInfo("❌ REJECTED: Company '%s' found in WRONG position '%s' (should be issuer, not customer/payer)",
					aiResult.CompanyNameInTemplate, aiResult.CompanyLocationInDoc)
				return TemplateMatchResult{
					Confidence: 0,
					Reason: fmt.Sprintf("Company '%s' is customer/payer (in '%s'), not document issuer",
						aiResult.CompanyNameInTemplate, aiResult.CompanyLocationInDoc),
					TokenUsage: tokenUsage, // 🔥 Include token usage even when rejected
				}
			}
		}

		// Also check is_company_issuer flag
		if !aiResult.IsCompanyIssuer {
			reqCtx.LogInfo("❌ REJECTED: AI marked company as NOT issuer (is_company_issuer=false)")
			return TemplateMatchResult{
				Confidence: 0,
				Reason: fmt.Sprintf("Company '%s' is not document issuer according to AI analysis",
					aiResult.CompanyNameInTemplate),
				TokenUsage: tokenUsage, // 🔥 Include token usage even when rejected
			}
		}

		if !isValid {
			reqCtx.LogInfo("⚠️  WARNING: Company location '%s' is ambiguous, relying on is_company_issuer flag",
				aiResult.CompanyLocationInDoc)
		}
	}

	// Find the matched template from map
	// Try exact match first
	matchedTemplate, found := templateMap[aiResult.MatchedTemplate]
	matchedDescription := aiResult.MatchedTemplate

	// If not found, try fuzzy matching (handle typos/variations)
	if !found {
		reqCtx.LogInfo("⚠️  Exact match failed for: '%s', trying fuzzy matching...", aiResult.MatchedTemplate)

		// Normalize AI's response
		aiTemplateLower := strings.ToLower(strings.TrimSpace(aiResult.MatchedTemplate))

		// Try to find similar template description
		bestSimilarity := 0.0
		var bestTemplate bson.M
		bestDescription := ""

		for desc, tmpl := range templateMap {
			descLower := strings.ToLower(strings.TrimSpace(desc))

			// Calculate similarity
			similarity := calculateStringSimilarity(aiTemplateLower, descLower)

			// If very similar (>75%), consider it a match
			if similarity > bestSimilarity {
				bestSimilarity = similarity
				bestTemplate = tmpl
				bestDescription = desc
			}
		}

		// Accept match if similarity > 75%
		if bestSimilarity > 0.75 {
			reqCtx.LogInfo("✅ Fuzzy match found: '%s' (similarity: %.1f%%)", bestDescription, bestSimilarity*100)
			matchedTemplate = bestTemplate
			matchedDescription = bestDescription
			found = true
		} else {
			reqCtx.LogInfo("❌ No similar template found (best: %.1f%%)", bestSimilarity*100)
			return TemplateMatchResult{
				Confidence: 0,
				Reason:     fmt.Sprintf("AI เลือก template '%s' ที่ไม่พบในระบบ (similarity: %.1f%%)", aiResult.MatchedTemplate, bestSimilarity*100),
				TokenUsage: tokenUsage, // 🔥 Include token usage even when not found
			}
		}
	}

	// Extract original description (before |) for return value
	originalDescription := matchedDescription
	if strings.Contains(matchedDescription, " | ") {
		parts := strings.Split(matchedDescription, " | ")
		originalDescription = strings.TrimSpace(parts[0])
	}

	// Return AI's decision
	bestMatch := TemplateMatchResult{
		Template:        matchedTemplate,
		Confidence:      float64(aiResult.Confidence),
		MatchedKeywords: []string{}, // AI doesn't use keywords
		Description:     originalDescription,
		TemplateID:      matchedTemplate["_id"],
		Reason:          aiResult.Reasoning,
		TokenUsage:      tokenUsage, // 🔥 Include token usage
	}

	if bestMatch.Confidence > 0 {
		reqCtx.LogInfo("✅ Best Match: '%s' (%.1f%%)", bestMatch.Description, bestMatch.Confidence)
	} else {
		reqCtx.LogInfo("❌ No template matched")
	}

	return bestMatch
}

// calculateTemplateScore คำนวณคะแนนการจับคู่ระหว่าง document กับ template
//
// NEW Algorithm - AI-Driven from template.description:
// 1. Extract keywords จาก template.description (user-defined)
// 2. ค้นหา keywords เหล่านั้นใน document text
// 3. คำนวณ fuzzy similarity สำหรับแต่ละ keyword
// 4. ให้คะแนนตามความใกล้เคียง
//
// Scoring:
// - Exact match: 40 points per keyword
// - Fuzzy match (85%+): 30 points per keyword
// - Partial match (70%+): 20 points per keyword
// - Overall fuzzy: +20 bonus
func calculateTemplateScore(
	docText string,
	templateDesc string,
) (score float64, matchedKeywords []string, reason string) {
	templateDesc = normalizeText(templateDesc)
	matchedKw := []string{}
	reasons := []string{}

	// Extract keywords from template description (user-defined in MongoDB)
	templateKeywords := extractKeywordsFromDescription(templateDesc)

	if len(templateKeywords) == 0 {
		return 0, []string{}, "no keywords in description"
	}

	// Check each keyword against document
	keywordScores := []float64{}

	for _, keyword := range templateKeywords {
		keywordLower := strings.ToLower(keyword)
		bestMatch := 0.0

		// Method 1: Exact match
		if strings.Contains(docText, keywordLower) {
			score += 40
			matchedKw = append(matchedKw, keyword)
			reasons = append(reasons, fmt.Sprintf("exact:%s", keyword))
			keywordScores = append(keywordScores, 1.0)
			continue
		}

		// Method 2: Fuzzy match - ค้นหาคำที่ใกล้เคียงใน document
		docWords := strings.Fields(docText)
		for _, docWord := range docWords {
			if len(docWord) < 2 {
				continue
			}

			// Calculate similarity
			similarity := calculateFuzzyMatch(keywordLower, docWord)

			if similarity > bestMatch {
				bestMatch = similarity
			}

			// Stop if found very good match
			if similarity > 0.9 {
				break
			}
		}

		// Score based on best match
		if bestMatch >= 0.85 {
			score += 30
			matchedKw = append(matchedKw, keyword)
			reasons = append(reasons, fmt.Sprintf("fuzzy:%s(%.0f%%)", keyword, bestMatch*100))
			keywordScores = append(keywordScores, bestMatch)
		} else if bestMatch >= 0.7 {
			score += 20
			matchedKw = append(matchedKw, keyword)
			reasons = append(reasons, fmt.Sprintf("partial:%s(%.0f%%)", keyword, bestMatch*100))
			keywordScores = append(keywordScores, bestMatch)
		}
	}

	// Bonus: Overall text similarity (ถ้า description สั้น เช่น "ค่าน้ำมัน")
	if len(templateDesc) < 50 {
		overallSimilarity := 0.0
		templateWords := strings.Fields(templateDesc)

		for _, tmplWord := range templateWords {
			if len(tmplWord) < 2 {
				continue
			}

			if strings.Contains(docText, tmplWord) {
				overallSimilarity += 1.0
			} else {
				// Check fuzzy
				docWords := strings.Fields(docText)
				for _, docWord := range docWords {
					sim := calculateFuzzyMatch(tmplWord, docWord)
					if sim > overallSimilarity {
						overallSimilarity = sim
					}
				}
			}
		}

		if overallSimilarity > 0.8 && len(templateWords) > 0 {
			bonus := 20.0 * (overallSimilarity / float64(len(templateWords)))
			score += bonus
			reasons = append(reasons, fmt.Sprintf("bonus:+%.0f", bonus))
		}
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}

	finalReason := strings.Join(reasons, ", ")
	if finalReason == "" {
		finalReason = "no match"
	}

	return score, matchedKw, finalReason
}

// contains helper function
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// calculateFuzzyMatch คำนวณความคล้ายกันด้วย Levenshtein Distance
// Return: similarity score 0.0-1.0
func calculateFuzzyMatch(text1, text2 string) float64 {
	// ถ้าข้อความยาวมาก ให้ตัดเอาส่วนที่สำคัญ
	if len(text1) > 200 {
		text1 = text1[:200]
	}
	if len(text2) > 200 {
		text2 = text2[:200]
	}

	distance := levenshteinDistance(text1, text2)
	maxLen := float64(max(len(text1), len(text2)))

	if maxLen == 0 {
		return 0
	}

	similarity := 1.0 - (float64(distance) / maxLen)
	return math.Max(0, similarity)
}

// levenshteinDistance คำนวณ edit distance ระหว่าง 2 strings
// Algorithm: Dynamic Programming
func levenshteinDistance(s1, s2 string) int {
	len1 := len(s1)
	len2 := len(s2)

	// Create matrix
	matrix := make([][]int, len1+1)
	for i := range matrix {
		matrix[i] = make([]int, len2+1)
	}

	// Initialize first row and column
	for i := 0; i <= len1; i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}

			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len1][len2]
}

// calculateSemanticMatch คำนวณคะแนนจากความหมายที่เกี่ยวข้อง
// เช่น "น้ำมัน" กับ "เชื้อเพลิง", "ไฟฟ้า" กับ "พลังงาน"
func calculateSemanticMatch(keywords []string, templateDesc string) float64 {
	// Semantic pairs (คำที่มีความหมายเกี่ยวข้อง)
	semanticPairs := map[string][]string{
		"น้ำมัน":       {"เชื้อเพลิง", "ดีเซล", "เบนซิน", "น้ำมัน"},
		"ไฟฟ้า":        {"พลังงาน", "ค่าไฟ", "electricity"},
		"อินเตอร์เน็ต": {"internet", "เน็ต", "บรอดแบนด์"},
		"ทำบัญชี":      {"บัญชี", "accounting", "ที่ปรึกษา"},
		"เงินเดือน":    {"ค่าจ้าง", "salary", "wage"},
		"ค่าเช่า":      {"เช่า", "rent", "rental"},
	}

	score := 0.0
	for _, keyword := range keywords {
		if relatedWords, exists := semanticPairs[keyword]; exists {
			for _, related := range relatedWords {
				if strings.Contains(templateDesc, related) {
					score += 10
					break
				}
			}
		}
	}

	return score
}

// containsPartial ตรวจสอบว่ามี substring ที่ใกล้เคียงหรือไม่
func containsPartial(text, keyword string, threshold float64) bool {
	if len(keyword) < 3 {
		return false
	}

	// Split text into words
	words := strings.Fields(text)
	for _, word := range words {
		if len(word) < 3 {
			continue
		}

		// Calculate similarity
		similarity := calculateFuzzyMatch(word, keyword)
		if similarity >= threshold {
			return true
		}
	}

	return false
}

// extractKeywordsFromDescription สกัดคำสำคัญจาก template description
// ไม่ใช้ hardcoded keywords - ให้ AI-driven โดยดูจาก description จริง
func extractKeywordsFromDescription(description string) []string {
	// Normalize
	description = normalizeText(description)

	// Split into words and filter meaningful ones (length >= 2)
	words := strings.Fields(description)
	keywords := []string{}

	// Stop words ที่ไม่สำคัญ (common Thai words)
	stopWords := map[string]bool{
		"การ": true, "ของ": true, "ที่": true, "จาก": true,
		"และ": true, "หรือ": true, "กับ": true, "ใน": true,
		"บริษัท": true, "จำกัด": true, "มหาชน": true,
		"ค่า": true, "เป็น": true, "มี": true, "ได้": true,
		"ไป": true, "มา": true, "ให้": true, "แล้ว": true,
	}

	for _, word := range words {
		// Skip short words and stop words
		if len(word) < 2 || stopWords[word] {
			continue
		}

		// Keep meaningful words
		keywords = append(keywords, word)
	}

	return keywords
}

// normalizeText ทำให้ข้อความเป็นมาตรฐาน (lowercase, remove extra spaces)
func normalizeText(text string) string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Remove extra whitespace
	text = strings.Join(strings.Fields(text), " ")

	// Remove non-alphanumeric (except Thai)
	result := strings.Builder{}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			result.WriteRune(r)
		}
	}

	return result.String()
}

// callGeminiForTemplateMatch calls Gemini AI for intelligent template matching
// Moved from ai package to avoid import cycle
func callGeminiForTemplateMatch(documentText string, templateDescriptions []string, reqCtx *common.RequestContext) (*aiTemplateMatchResult, *common.TokenUsage, error) {
	// Step 1: Initialize the Gemini client
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(configs.GEMINI_API_KEY))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	defer client.Close()

	// Use Template Matching-specific model for Phase 2
	model := client.GenerativeModel(configs.TEMPLATE_MODEL_NAME)
	reqCtx.LogInfo("🔍 Phase 2 - Template Model: %s", configs.TEMPLATE_MODEL_NAME)

	// Step 2: Define the JSON schema for template matching
	schema := createTemplateMatchSchemaLocal()

	// Step 3: Configure the model with JSON response
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = schema

	// Step 4: Build the prompt
	prompt := getTemplateMatchingPromptLocal(documentText, templateDescriptions)

	// Step 5: Call Gemini API with retry logic for 429 errors
	// Apply rate limiting to prevent 429 errors
	ratelimit.WaitForRateLimit()
	reqCtx.LogInfo("📤 ส่งคำขอ Template Matching ไปยัง Gemini AI...")

	// Retry up to 3 times with exponential backoff for 429 errors
	var resp *genai.GenerateContentResponse
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err = model.GenerateContent(ctx, genai.Text(prompt))
		if err == nil {
			break
		}

		// Check if it's a 429 error
		errMsg := err.Error()
		if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "Resource exhausted") {
			if attempt < maxRetries {
				waitTime := time.Duration(attempt*10) * time.Second
				reqCtx.LogWarning("⚠️  Rate limit (429), waiting %v before retry (attempt %d/%d)", waitTime, attempt, maxRetries)
				time.Sleep(waitTime)
				continue
			}
		}
		break
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate content after %d attempts: %w", maxRetries, err)
	}
	reqCtx.LogInfo("📥 ได้รับ response จาก Gemini AI")

	// Extract the JSON response
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, nil, fmt.Errorf("no response from Gemini API")
	}

	// Get the text response
	var jsonResponse string
	for _, part := range resp.Candidates[0].Content.Parts {
		if text, ok := part.(genai.Text); ok {
			jsonResponse = string(text)
			break
		}
	}

	if jsonResponse == "" {
		return nil, nil, fmt.Errorf("empty response from Gemini API")
	}

	// Step 6: Unmarshal the JSON into aiTemplateMatchResult struct
	var result aiTemplateMatchResult
	if err := json.Unmarshal([]byte(jsonResponse), &result); err != nil {
		// Log the problematic JSON response for debugging
		preview := jsonResponse
		if len(preview) > 300 {
			preview = preview[:300] + "... (truncated)"
		}
		reqCtx.LogInfo("⚠️  Failed to parse template match JSON. Preview: %s", preview)
		return nil, nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	// Step 7: Extract token usage using Template-specific pricing (Phase 2)
	var tokenUsage *common.TokenUsage
	if resp.UsageMetadata != nil {
		// 📊 DETAILED TOKEN BREAKDOWN (Phase 2 - Template Matching)
		reqCtx.LogInfo("═══════════════════════════════════════════════════")
		reqCtx.LogInfo("📊 PHASE 2 - TEMPLATE MATCHING TOKEN BREAKDOWN")
		reqCtx.LogInfo("═══════════════════════════════════════════════════")
		reqCtx.LogInfo("📝 Prompt Tokens: %d", resp.UsageMetadata.PromptTokenCount)

		// Calculate input tokens including thought tokens (billed as input)
		inputTokens := int(resp.UsageMetadata.PromptTokenCount)

		if resp.UsageMetadata.CachedContentTokenCount > 0 {
			inputTokens += int(resp.UsageMetadata.CachedContentTokenCount)
			reqCtx.LogInfo("💾 Cached Tokens: %d (billed as input)", resp.UsageMetadata.CachedContentTokenCount)
		}

		// Check for thought tokens (Gemini 2.5 Flash thinking mode)
		if resp.UsageMetadata.TotalTokenCount > (resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount) {
			thoughtTokens := resp.UsageMetadata.TotalTokenCount - resp.UsageMetadata.PromptTokenCount - resp.UsageMetadata.CandidatesTokenCount
			reqCtx.LogInfo("💭 Thought Tokens: %d (billed as input)", thoughtTokens)
			inputTokens += int(thoughtTokens)
		}

		reqCtx.LogInfo("📤 Output Tokens: %d", resp.UsageMetadata.CandidatesTokenCount)
		reqCtx.LogInfo("📊 Total Tokens: %d", resp.UsageMetadata.TotalTokenCount)

		tokens := common.CalculateTemplateTokenCost(
			inputTokens,
			int(resp.UsageMetadata.CandidatesTokenCount),
		)
		tokenUsage = &tokens

		// 💰 COST BREAKDOWN
		reqCtx.LogInfo("───────────────────────────────────────────────────")
		reqCtx.LogInfo("💰 COST CALCULATION (Phase 2 - Template):")
		reqCtx.LogInfo("   Model: %s", configs.TEMPLATE_MODEL_NAME)
		reqCtx.LogInfo("   Input Cost:  %d tokens × $%.4f/1M = $%.6f", inputTokens, configs.TEMPLATE_INPUT_PRICE_PER_MILLION, float64(inputTokens)*configs.TEMPLATE_INPUT_PRICE_PER_MILLION/1_000_000)
		reqCtx.LogInfo("   Output Cost: %d tokens × $%.4f/1M = $%.6f", resp.UsageMetadata.CandidatesTokenCount, configs.TEMPLATE_OUTPUT_PRICE_PER_MILLION, float64(resp.UsageMetadata.CandidatesTokenCount)*configs.TEMPLATE_OUTPUT_PRICE_PER_MILLION/1_000_000)
		reqCtx.LogInfo("   Total USD: $%.6f", tokens.CostUSD)
		reqCtx.LogInfo("   Total THB: ฿%.4f", tokens.CostTHB)
		reqCtx.LogInfo("═══════════════════════════════════════════════════")
	}

	reqCtx.LogInfo("✅ AI Template Matching: '%s' (%d%%) - %s", result.MatchedTemplate, result.Confidence, result.Reasoning)

	return &result, tokenUsage, nil
}

// getTemplateMatchingPromptLocal creates a prompt for AI-based template matching (local copy to avoid import cycle)
func getTemplateMatchingPromptLocal(documentText string, templateDescriptions []string) string {
	prompt := `
คุณคือผู้เชี่ยวชาญด้านการจับคู่เอกสารบัญชี

🎯 **งานของคุณ: วิเคราะห์เอกสารและหา Template ที่ตรงที่สุด**

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📄 ข้อความจากเอกสาร (OCR)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

` + documentText + `

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📋 Template ทางบัญชีที่มีในระบบ
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ℹ️ Format: Template Description | Additional Context (if available)

`

	for i, desc := range templateDescriptions {
		prompt += fmt.Sprintf("%d. %s\n", i+1, desc)
	}

	prompt += `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🧠 วิธีการวิเคราะห์
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

**ขั้นตอนที่ 1: ทำความเข้าใจเอกสาร**
- อ่านเอกสารทั้งหมด
- ระบุ "ผู้ออกเอกสาร" (Issuer) - มักอยู่หัวเอกสาร
- ระบุ "ลูกค้า/ผู้รับบริการ" (Customer) - มักมีคำว่า "ชื่อลูกค้า"
- ระบุ "ประเภทสินค้า/บริการ"
- ระบุ "ทิศทาง": เราเป็นผู้ขาย หรือ เราเป็นผู้ซื้อ?

**ขั้นตอนที่ 2: วิเคราะห์ Template**
- อ่าน template description และ additional context (หลัง | )
- ดูว่า template มีข้อมูลเฉพาะเจาะจงอะไรบ้าง (เช่น ชื่อบริษัท, ประเภทสินค้า)
- เข้าใจว่า template ใช้สำหรับสถานการณ์แบบไหน
- ดูจาก "สมุดรายวัน" (ถ้ามี): 
  * รายวันรับ (IV) = เรารับเงิน = เราขาย
  * รายวันจ่าย (PV) = เราจ่ายเงิน = เราซื้อ

**ขั้นตอนที่ 3: 🚨 CRITICAL - ตรวจสอบตำแหน่งชื่อบริษัท (ถ้า template ระบุชื่อบริษัท)**

⚠️ กฎสำคัญที่สุด: ถ้า Template Description มีชื่อบริษัท/ร้าน/หน่วยงานเฉพาะเจาะจง:

1. ระบุชื่อบริษัทที่อยู่ใน template → บันทึกใน company_name_in_template

2. หาตำแหน่งที่พบชื่อบริษัทในเอกสาร → บันทึกใน company_location_in_doc:
   - "document_header" = อยู่หัวเอกสาร/บรรทัดแรก
   - "received_from" = อยู่ในช่อง "ได้รับเงินจาก" (สำหรับใบเสร็จรับเงิน)
   - "customer_name" = อยู่ในช่อง "ชื่อลูกค้า", "NAME", "CUSTOMER"
   - "bill_to" = อยู่ใน "BILL TO", "SHIP TO"
   - "not_found" = ไม่พบชื่อบริษัทในเอกสาร

3. ตัดสินใจว่าบริษัทเป็นผู้ออกหรือไม่ → บันทึกใน is_company_issuer:
   - true = บริษัทเป็นผู้ออกเอกสาร (หัวเอกสาร, ผู้รับเงิน, ผู้ขาย)
   - false = บริษัทเป็นลูกค้า/ผู้จ่าย/ผู้ซื้อ

🚨 เงื่อนไขบังคับ:
- ถ้า company_location_in_doc = "received_from" → ต้องให้ is_company_issuer = false
- ถ้า company_location_in_doc = "customer_name" → ต้องให้ is_company_issuer = false
- ถ้า company_location_in_doc = "document_header" → ต้องให้ is_company_issuer = true
- ถ้า is_company_issuer = false → ต้องให้ confidence = 0 (ห้ามใช้ template นี้!)

📌 ตัวอย่างสำคัญ (ใบเสร็จรับเงิน):

เอกสาร:
  เทศบาลตำบลหนองป่าครั่ง (หัวเอกสาร)
  ใบเสร็จรับเงิน
  ได้รับเงินจาก: บริษัท นพรัตน์กู๊ดไทร์ (ผู้จ่ายเงิน!)

Template: "ใบเสร็จรับเงิน ชื่อหัวบิล คือ บริษัท นพรัตน์กู๊ดไทร์"

✅ การตอบที่ถูกต้อง (JSON):
{
  "matched_template": "ใบเสร็จรับเงิน ชื่อหัวบิล คือ บริษัท นพรัตน์กู๊ดไทร์",
  "confidence": 0,
  "reasoning": "บริษัท นพรัตน์ปรากฏในช่อง 'ได้รับเงินจาก' (เป็นผู้จ่ายเงิน) ไม่ใช่ผู้ออกเอกสาร",
  "company_name_in_template": "บริษัท นพรัตน์กู๊ดไทร์",
  "company_location_in_doc": "received_from",
  "is_company_issuer": false
}

❌ การตอบที่ผิด (JSON):
{
  "confidence": 100,
  "is_company_issuer": true
}


**ขั้นตอนที่ 4: เปรียบเทียบและตัดสินใจ**

✅ ตรงกับ template เมื่อ:
   - เอกสารและ template เป็นประเภทเดียวกัน
   - ถ้า template ระบุชื่อบริษัท → is_company_issuer = true
   - ถ้า template ระบุสินค้า/บริการ → ต้องตรงกับที่ระบุ
   - ทิศทางธุรกรรมสอดคล้องกัน (ขาย/ซื้อ)

❌ ไม่ตรง template เมื่อ:
   - บริษัทที่ระบุใน template เป็นเพียง "ลูกค้า" ในเอกสาร (is_company_issuer = false)
   - ประเภทสินค้า/บริการไม่ตรงกัน
   - ทิศทางธุรกรรมตรงข้าม

Additional context ให้ข้อมูลเพิ่มเติม ใช้วิเคราะห์ประกอบการตัดสินใจ

**ตัวอย่างการวิเคราะห์:**

✅ ถ้าเอกสารมี:
  - "เบนซิน", "ดีเซล", "แก๊สโซฮอล์", "ปั๊ม", "ลิตร"
  - ชื่อบริษัทน้ำมัน: "ปตท", "บางจาก", "เชลล์", "เอสโซ่"
  → ตรงกับ: **"ค่าน้ำมัน"** (90-100%)

✅ ถ้าเอกสารมี:
  - "การไฟฟ้า", "หน่วย", "kWh", "PEA", "MEA"
  - "ค่าไฟ", "เลขมิเตอร์"
  → ตรงกับ: **"ค่าไฟฟ้า"** (90-100%)

⚠️ **สำคัญ:**
- ให้คะแนน confidence 0-100%
- ต่ำกว่า 60% = ไม่แน่ใจ (ควรใช้ full mode)
- 60-94% = ค่อนข้างแน่ใจ (แต่ยังไม่ผ่านเกณฑ์)
- 95-100% = แน่ใจมาก (ใช้ template-only mode ได้)
- ถ้าไม่มี template ไหนเข้าข่าย ให้เลือก "ค่าใช้จ่ายเบ็ดเตล็ด" แทน

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📤 ตอบกลับ
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ให้ตอบเป็น JSON format:
- matched_template: ชื่อ template ที่ตรงที่สุด (string)
- confidence: ความมั่นใจ 0-100 (integer) - **ต้องเป็น 0 ถ้า is_company_issuer = false**
- reasoning: เหตุผลที่เลือก template นี้ (string, สั้นๆ ภาษาไทย)
- company_name_in_template: ชื่อบริษัทที่ระบุใน template (string) - ถ้าไม่มีให้ใส่ ""
- company_location_in_doc: ตำแหน่งที่พบชื่อบริษัทในเอกสาร (string) - "document_header", "received_from", "customer_name", "bill_to", "not_found"
- is_company_issuer: บริษัทเป็นผู้ออกเอกสารหรือไม่ (boolean) - true = ผู้ออก, false = ลูกค้า/ผู้จ่าย
`

	return prompt
}

// createTemplateMatchSchemaLocal creates the JSON schema for AI template matching (local copy to avoid import cycle)
func createTemplateMatchSchemaLocal() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"matched_template": {
				Type:        genai.TypeString,
				Description: "ชื่อ template ที่ตรงที่สุดกับเอกสาร (ต้องตรงกับ description ที่ให้มาเท่านั้น)",
			},
			"confidence": {
				Type:        genai.TypeInteger,
				Description: "ความมั่นใจ 0-100 (ต้องเป็น 0 ถ้า is_company_issuer = false, ต่ำกว่า 60 = ไม่แน่ใจ, 60-94 = ค่อนข้างแน่ใจ, 95-100 = แน่ใจมาก)",
			},
			"reasoning": {
				Type:        genai.TypeString,
				Description: "เหตุผลที่เลือก template นี้ (สั้นๆ ภาษาไทย)",
			},
			"company_name_in_template": {
				Type:        genai.TypeString,
				Description: "ชื่อบริษัทที่ระบุใน template description (ถ้าไม่มีให้ใส่ \"\")",
			},
			"company_location_in_doc": {
				Type:        genai.TypeString,
				Description: "ตำแหน่งที่พบชื่อบริษัทในเอกสาร: document_header, received_from, customer_name, bill_to, not_found",
			},
			"is_company_issuer": {
				Type:        genai.TypeBoolean,
				Description: "บริษัทเป็นผู้ออกเอกสารหรือไม่ (true = ผู้ออก/ผู้รับเงิน, false = ลูกค้า/ผู้จ่ายเงิน) - ถ้า false ต้องให้ confidence = 0",
			},
		},
		Required: []string{"matched_template", "confidence", "reasoning", "company_name_in_template", "company_location_in_doc", "is_company_issuer"},
	}
}

// Helper functions
func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// calculateStringSimilarity calculates similarity between two strings (0.0 - 1.0)
// Uses a combination of:
// 1. Exact substring matching (keywords overlap)
// 2. Length similarity
// 3. Common word ratio
func calculateStringSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	// Tokenize into words
	words1 := strings.Fields(s1)
	words2 := strings.Fields(s2)

	if len(words1) == 0 || len(words2) == 0 {
		return 0.0
	}

	// Count common words
	commonCount := 0
	for _, w1 := range words1 {
		for _, w2 := range words2 {
			if w1 == w2 {
				commonCount++
				break
			}
		}
	}

	// Calculate Jaccard similarity (intersection / union)
	totalWords := len(words1) + len(words2) - commonCount
	if totalWords == 0 {
		return 0.0
	}

	jaccardSim := float64(commonCount) / float64(totalWords)

	// Bonus for substring containment
	containmentBonus := 0.0
	if strings.Contains(s1, s2) || strings.Contains(s2, s1) {
		containmentBonus = 0.2
	}

	// Combine scores
	similarity := jaccardSim + containmentBonus
	if similarity > 1.0 {
		similarity = 1.0
	}

	return similarity
}
