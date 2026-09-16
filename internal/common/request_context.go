// request_context.go - Request tracking and logging system

package common

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// RequestContext tracks the entire request lifecycle with timing and costs
type RequestContext struct {
	RequestID           string
	ShopID              string
	StartTime           time.Time
	Steps               []StepLog
	TotalTokens         TokenUsage
	CurrentStep         string
	CurrentStepStart    time.Time
	CurrentSubSteps     []SubStepLog
	CurrentSubStep      string
	CurrentSubStepStart time.Time
}

// StepLog represents a single processing step
type StepLog struct {
	Name      string       `json:"name"`
	StartTime time.Time    `json:"start_time"`
	Duration  int64        `json:"duration_ms"`
	Status    string       `json:"status"` // "success", "failed", "skipped"
	Tokens    *TokenUsage  `json:"tokens,omitempty"`
	Error     string       `json:"error,omitempty"`
	SubSteps  []SubStepLog `json:"sub_steps,omitempty"`
}

// SubStepLog represents a detailed sub-operation within a step
type SubStepLog struct {
	Name      string    `json:"name"`
	StartTime time.Time `json:"start_time"`
	Duration  int64     `json:"duration_ms"`
	Details   string    `json:"details,omitempty"`
}

// TokenUsage tracks API token consumption
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// No cost fields, and no pricing table behind them — see the comment in
// configs/config.go for why a self-computed baht figure was removed rather
// than repaired. Token counts below come straight from the provider.

// NewRequestContext creates a new request tracking context
func NewRequestContext(shopID string) *RequestContext {
	reqID := uuid.New().String()
	now := time.Now()

	log.Printf("[%s] 🚀 เริ่มรับคำขอใหม่ | ShopID: %s | เวลา: %s", reqID, shopID, now.Format("15:04:05"))

	return &RequestContext{
		RequestID:   reqID,
		ShopID:      shopID,
		StartTime:   now,
		Steps:       []StepLog{},
		TotalTokens: TokenUsage{},
	}
}

// StartStep begins tracking a new processing step
func (rc *RequestContext) StartStep(stepName string) {
	rc.CurrentStep = stepName
	rc.CurrentStepStart = time.Now()

	// Map step names to Thai descriptions
	stepDescriptions := map[string]string{
		"download_images":               "📷 ดาวน์โหลดรูปภาพ",
		"full_ocr_extraction_all":       "🔍 อ่านข้อมูลจากใบเสร็จ (Full OCR)",
		"prepare_master_data":           "📊 เตรียมข้อมูลหลัก (Master Data)",
		"phase2_multi_image_accounting": "💼 วิเคราะห์รายการบัญชี (AI Analysis)",
	}

	desc := stepDescriptions[stepName]
	if desc == "" {
		desc = stepName
	}

	log.Printf("[%s] \n┌── %s", rc.RequestID, desc)
}

// EndStep completes the current step and records timing
func (rc *RequestContext) EndStep(status string, tokens *TokenUsage, err error) {
	duration := time.Since(rc.CurrentStepStart).Milliseconds()

	stepLog := StepLog{
		Name:      rc.CurrentStep,
		StartTime: rc.CurrentStepStart,
		Duration:  duration,
		Status:    status,
		Tokens:    tokens,
		SubSteps:  rc.CurrentSubSteps, // Capture sub-steps
	}

	if err != nil {
		stepLog.Error = err.Error()
		log.Printf("[%s] ❌ FAILED - %s (%.2fs) - Error: %v",
			rc.RequestID, rc.CurrentStep, float64(duration)/1000, err)
	} else {
		logMsg := fmt.Sprintf("[%s] └── ✅ สำเร็จ: %.2fวิ",
			rc.RequestID, float64(duration)/1000)

		if tokens != nil {
			rc.TotalTokens.InputTokens += tokens.InputTokens
			rc.TotalTokens.OutputTokens += tokens.OutputTokens
			rc.TotalTokens.TotalTokens += tokens.TotalTokens

			logMsg += fmt.Sprintf(" | 🪙 Tokens: %dเข้า + %dออก = %d",
				tokens.InputTokens, tokens.OutputTokens, tokens.TotalTokens)
		}

		// Log sub-steps summary if any
		if len(rc.CurrentSubSteps) > 0 {
			logMsg += fmt.Sprintf(" | ขั้นย่อย: %d", len(rc.CurrentSubSteps))
		}

		log.Printf("%s", logMsg)
	}

	rc.Steps = append(rc.Steps, stepLog)
	rc.CurrentStep = ""
	rc.CurrentSubSteps = []SubStepLog{} // Reset sub-steps for next step
}

// CalculateTokenCost computes USD and THB cost from token counts
// Deprecated: Use phase-specific functions (CalculateOCRTokenCost, CalculateTemplateTokenCost, etc.)
// Falls back to OCR pricing for backward compatibility
func CalculateTokenCost(inputTokens, outputTokens int) TokenUsage {
	// Use OCR pricing as default fallback
	return CalculateOCRTokenCost(inputTokens, outputTokens)
}

// CalculateOCRTokenCost calculates cost for Phase 1 (OCR) using OCR-specific pricing
func CalculateOCRTokenCost(inputTokens, outputTokens int) TokenUsage {
	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}

// CalculateTemplateTokenCost calculates cost for Phase 2 (Template Matching)
func CalculateTemplateTokenCost(inputTokens, outputTokens int) TokenUsage {
	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}

// CalculateTemplateAccountingTokenCost calculates cost for Phase 3 (Template-only mode)
// Uses Flash-Lite pricing (faster & cheaper for high-confidence template matches)
func CalculateTemplateAccountingTokenCost(inputTokens, outputTokens int) TokenUsage {
	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}

// CalculateAccountingTokenCost calculates cost for Phase 3 (Full analysis mode)
// Uses Flash pricing (better reasoning for low-confidence or complex cases)
func CalculateAccountingTokenCost(inputTokens, outputTokens int) TokenUsage {
	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}

// GetSummary returns a final summary of the entire request
func (rc *RequestContext) GetSummary() map[string]interface{} {
	totalDuration := time.Since(rc.StartTime).Milliseconds()

	// Build step breakdown with token/cost details
	stepBreakdown := make(map[string]int64)
	phaseDetails := make([]map[string]interface{}, 0)
	apiCallCount := 0

	for _, step := range rc.Steps {
		stepBreakdown[step.Name] = step.Duration

		// Count API calls and collect phase details
		if step.Tokens != nil {
			apiCallCount++
			phaseDetails = append(phaseDetails, map[string]interface{}{
				"phase":         step.Name,
				"duration_sec":  float64(step.Duration) / 1000,
				"input_tokens":  step.Tokens.InputTokens,
				"output_tokens": step.Tokens.OutputTokens,
				"total_tokens":  step.Tokens.TotalTokens,
			})
		}
	}

	summary := map[string]interface{}{
		"request_id":         rc.RequestID,
		"shop_id":            rc.ShopID,
		"total_duration_ms":  totalDuration,
		"total_duration_sec": float64(totalDuration) / 1000,
		"step_breakdown":     stepBreakdown,
		"total_steps":        len(rc.Steps),
		"api_calls":          apiCallCount,
		"phase_details":      phaseDetails,
		"token_usage": map[string]interface{}{
			"input_tokens":  rc.TotalTokens.InputTokens,
			"output_tokens": rc.TotalTokens.OutputTokens,
			"total_tokens":  rc.TotalTokens.TotalTokens,
		},
	}

	// Enhanced logging with phase breakdown
	log.Printf("[%s] ", rc.RequestID)
	log.Printf("[%s] ═══════════════════════════════════════════════════", rc.RequestID)
	log.Printf("[%s] 🎯 สรุปผลการประมวลผล", rc.RequestID)
	log.Printf("[%s] ═══════════════════════════════════════════════════", rc.RequestID)
	log.Printf("[%s] 📊 จำนวน API Calls: %d ครั้ง", rc.RequestID, apiCallCount)
	log.Printf("[%s] ", rc.RequestID)

	// Log each phase detail
	for i, phase := range phaseDetails {
		log.Printf("[%s] 🔹 รอบที่ %d: %s", rc.RequestID, i+1, phase["phase"])
		log.Printf("[%s]    ├─ Tokens: %d input + %d output = %d total",
			rc.RequestID,
			phase["input_tokens"],
			phase["output_tokens"],
			phase["total_tokens"])
	}

	log.Printf("[%s] ", rc.RequestID)
	log.Printf("[%s] ───────────────────────────────────────────────────", rc.RequestID)
	log.Printf("[%s] 🪙 สรุปการใช้ Token:", rc.RequestID)
	log.Printf("[%s]    ├─ Total Input Tokens:  %s", rc.RequestID, formatNumber(rc.TotalTokens.InputTokens))
	log.Printf("[%s]    ├─ Total Output Tokens: %s", rc.RequestID, formatNumber(rc.TotalTokens.OutputTokens))
	log.Printf("[%s]    └─ Total Tokens: %s", rc.RequestID, formatNumber(rc.TotalTokens.TotalTokens))
	log.Printf("[%s] ", rc.RequestID)
	log.Printf("[%s] 📌 ค่าใช้จ่ายจริงดูที่ Google AI Studio / Cloud Billing", rc.RequestID)
	log.Printf("[%s]    (บริการนี้ไม่คำนวณค่าเงินเอง — ดูเหตุผลใน configs/config.go)", rc.RequestID)
	log.Printf("[%s] ═══════════════════════════════════════════════════", rc.RequestID)
	log.Printf("[%s] ⏱️  เวลารวมทั้งหมด: %.2f วินาที", rc.RequestID, float64(totalDuration)/1000)
	log.Printf("[%s] ═══════════════════════════════════════════════════", rc.RequestID)

	return summary
}

// StartSubStep begins tracking a detailed sub-operation
func (rc *RequestContext) StartSubStep(subStepName string) {
	rc.CurrentSubStep = subStepName
	rc.CurrentSubStepStart = time.Now()

	// Map sub-step names to Thai
	subStepDesc := map[string]string{
		"image_preprocessing": "🔧 ปรับคุณภาพรูป",
		"init_gemini_client":  "🤖 เชื่อมต่อ AI",
		"create_json_schema":  "📝 สร้าง Schema",
		"configure_model":     "⚙️ ตั้งค่า AI Model",
		"build_prompt":        "📢 สร้างคำสั่ง Prompt",
		"call_gemini_api":     "🚀 เรียก Gemini API",
		"parse_json_response": "🔄 แปลงผลลัพธ์",
		"extract_metadata":    "📊 ดึงข้อมูล Metadata",
	}

	desc := subStepDesc[subStepName]
	if desc == "" {
		desc = subStepName
	}

	log.Printf("[%s]    ├─ %s...", rc.RequestID, desc)
}

// EndSubStep completes the current sub-step and records timing
func (rc *RequestContext) EndSubStep(details string) {
	if rc.CurrentSubStep == "" {
		return
	}

	duration := time.Since(rc.CurrentSubStepStart).Milliseconds()

	subStepLog := SubStepLog{
		Name:      rc.CurrentSubStep,
		StartTime: rc.CurrentSubStepStart,
		Duration:  duration,
		Details:   details,
	}

	rc.CurrentSubSteps = append(rc.CurrentSubSteps, subStepLog)

	detailsMsg := ""
	if details != "" {
		detailsMsg = " | " + details
	}
	log.Printf("[%s]    └─ ✅ %.2fวิ%s",
		rc.RequestID, float64(duration)/1000, detailsMsg)

	rc.CurrentSubStep = ""
}

// LogInfo logs info-level message with request ID prefix
func (rc *RequestContext) LogInfo(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] ℹ️  %s", rc.RequestID, msg)
}

// LogWarning logs warning-level message with request ID prefix
func (rc *RequestContext) LogWarning(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] ⚠️  %s", rc.RequestID, msg)
}

// LogError logs error-level message with request ID prefix
func (rc *RequestContext) LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] ❌ %s", rc.RequestID, msg)
}

// GetPartialSummary returns a summary of completed steps (for timeout scenarios)
func (rc *RequestContext) GetPartialSummary() map[string]interface{} {
	completedSteps := []string{}
	for _, step := range rc.Steps {
		if step.Status == "success" {
			completedSteps = append(completedSteps, step.Name)
		}
	}

	return map[string]interface{}{
		"completed_steps": completedSteps,
		"total_steps":     len(rc.Steps),
		"current_step":    rc.CurrentStep,
	}
}

// formatNumber adds comma separators to numbers
func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n%1000000)/1000, n%1000)
}
