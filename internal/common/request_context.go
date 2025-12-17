// request_context.go - Request tracking and logging system

package common

import (
	"fmt"
	"log"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
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
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	CostTHB      float64 `json:"cost_thb"`
}

// Pricing is now loaded from configs package to support different models
// Gemini 2.5 Flash-Lite: Input=$0.10, Output=$0.40
// Gemini 2.5 Flash: Input=$0.30, Output=$2.50

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
			rc.TotalTokens.CostUSD += tokens.CostUSD
			rc.TotalTokens.CostTHB += tokens.CostTHB

			logMsg += fmt.Sprintf(" | 🪙 Tokens: %dเข้า + %dออก = %d | 💰 ค่าใช้จ่าย: ฿%.2f",
				tokens.InputTokens, tokens.OutputTokens, tokens.TotalTokens, tokens.CostTHB)
		}

		// Log sub-steps summary if any
		if len(rc.CurrentSubSteps) > 0 {
			logMsg += fmt.Sprintf(" | ขั้นย่อย: %d", len(rc.CurrentSubSteps))
		}

		log.Printf(logMsg)
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
	totalTokens := inputTokens + outputTokens

	inputCost := float64(inputTokens) * configs.OCR_INPUT_PRICE_PER_MILLION / 1_000_000
	outputCost := float64(outputTokens) * configs.OCR_OUTPUT_PRICE_PER_MILLION / 1_000_000
	costUSD := inputCost + outputCost
	costTHB := costUSD * configs.USD_TO_THB

	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CostUSD:      costUSD,
		CostTHB:      costTHB,
	}
}

// CalculateTemplateTokenCost calculates cost for Phase 2 (Template Matching)
func CalculateTemplateTokenCost(inputTokens, outputTokens int) TokenUsage {
	totalTokens := inputTokens + outputTokens

	inputCost := float64(inputTokens) * configs.TEMPLATE_INPUT_PRICE_PER_MILLION / 1_000_000
	outputCost := float64(outputTokens) * configs.TEMPLATE_OUTPUT_PRICE_PER_MILLION / 1_000_000
	costUSD := inputCost + outputCost
	costTHB := costUSD * configs.USD_TO_THB

	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CostUSD:      costUSD,
		CostTHB:      costTHB,
	}
}

// CalculateTemplateAccountingTokenCost calculates cost for Phase 3 (Template-only mode)
// Uses Flash-Lite pricing (faster & cheaper for high-confidence template matches)
func CalculateTemplateAccountingTokenCost(inputTokens, outputTokens int) TokenUsage {
	totalTokens := inputTokens + outputTokens

	inputCost := float64(inputTokens) * configs.TEMPLATE_ACCOUNTING_INPUT_PRICE_PER_MILLION / 1_000_000
	outputCost := float64(outputTokens) * configs.TEMPLATE_ACCOUNTING_OUTPUT_PRICE_PER_MILLION / 1_000_000
	costUSD := inputCost + outputCost
	costTHB := costUSD * configs.USD_TO_THB

	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CostUSD:      costUSD,
		CostTHB:      costTHB,
	}
}

// CalculateAccountingTokenCost calculates cost for Phase 3 (Full analysis mode)
// Uses Flash pricing (better reasoning for low-confidence or complex cases)
func CalculateAccountingTokenCost(inputTokens, outputTokens int) TokenUsage {
	totalTokens := inputTokens + outputTokens

	inputCost := float64(inputTokens) * configs.ACCOUNTING_INPUT_PRICE_PER_MILLION / 1_000_000
	outputCost := float64(outputTokens) * configs.ACCOUNTING_OUTPUT_PRICE_PER_MILLION / 1_000_000
	costUSD := inputCost + outputCost
	costTHB := costUSD * configs.USD_TO_THB

	return TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CostUSD:      costUSD,
		CostTHB:      costTHB,
	}
}

// GetSummary returns a final summary of the entire request
func (rc *RequestContext) GetSummary() map[string]interface{} {
	totalDuration := time.Since(rc.StartTime).Milliseconds()

	// Build step breakdown
	stepBreakdown := make(map[string]int64)
	for _, step := range rc.Steps {
		stepBreakdown[step.Name] = step.Duration
	}

	summary := map[string]interface{}{
		"request_id":         rc.RequestID,
		"shop_id":            rc.ShopID,
		"total_duration_ms":  totalDuration,
		"total_duration_sec": float64(totalDuration) / 1000,
		"step_breakdown":     stepBreakdown,
		"total_steps":        len(rc.Steps),
		"token_usage": map[string]interface{}{
			"input_tokens":  rc.TotalTokens.InputTokens,
			"output_tokens": rc.TotalTokens.OutputTokens,
			"total_tokens":  rc.TotalTokens.TotalTokens,
			"cost_usd":      fmt.Sprintf("$%.4f", rc.TotalTokens.CostUSD),
			"cost_thb":      fmt.Sprintf("฿%.2f", rc.TotalTokens.CostTHB),
		},
	}

	log.Printf("[%s] \n═══ 🎯 สรุปผล ═══")
	log.Printf("[%s] ⏱️  เวลารวม: %.2fวินาที | 📝 ขั้นตอน: %d | 🪙 Tokens: %s | 💰 ค่าใช้จ่าย: ฿%.2f",
		rc.RequestID,
		float64(totalDuration)/1000,
		len(rc.Steps),
		fmt.Sprintf("%sเข้า + %sออก = %sรวม",
			formatNumber(rc.TotalTokens.InputTokens),
			formatNumber(rc.TotalTokens.OutputTokens),
			formatNumber(rc.TotalTokens.TotalTokens)),
		rc.TotalTokens.CostTHB)
	log.Printf("[%s] ═══════════════════════════\n", rc.RequestID)

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
