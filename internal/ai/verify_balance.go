// verify_balance.go - Narrow self-verification pass for unbalanced accounting entries.
//
// Phase 3 (ProcessMultiImageAccountingAnalysis) occasionally writes a debit/credit
// value that disagrees with its own selection_reason/side_reason text — e.g. the
// reasoning correctly states "426.93 - 11.97 = 414.96" but the credit field holds
// 412.96. This is a narrower task than the original analysis (compare text to a
// number, no image re-reading), so it gets its own cheap, focused call rather than
// re-running the full accounting analysis.

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/bosocmputer/account_ocr_gemini/internal/ratelimit"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// VerifyEntry is a package-local mirror of api.JournalEntry (account_code,
// account_name, debit, credit, selection_reason, side_reason). It exists so
// this package doesn't need to import internal/api, which would create an
// import cycle (api already imports ai).
type VerifyEntry struct {
	AccountCode     string  `json:"account_code"`
	AccountName     string  `json:"account_name"`
	Debit           float64 `json:"debit"`
	Credit          float64 `json:"credit"`
	SelectionReason string  `json:"selection_reason,omitempty"`
	SideReason      string  `json:"side_reason,omitempty"`
}

// EntriesShapeMatches checks that a verification pass only touched debit/credit
// magnitudes, not the entries' structure. Any structural difference (entry
// count, account code/order, or which side Dr/Cr the amount landed on) makes
// the correction untrustworthy — reject it entirely rather than partially
// trust a rewrite the model wasn't asked to do.
func EntriesShapeMatches(original, corrected []VerifyEntry) bool {
	if len(original) != len(corrected) {
		return false
	}
	for i := range original {
		if original[i].AccountCode != corrected[i].AccountCode {
			return false
		}
		origIsDebit := original[i].Debit > 0
		correctedIsDebit := corrected[i].Debit > 0
		origIsCredit := original[i].Credit > 0
		correctedIsCredit := corrected[i].Credit > 0
		if origIsDebit != correctedIsDebit || origIsCredit != correctedIsCredit {
			return false
		}
	}
	return true
}

// buildVerifyBalancePrompt builds a short, self-contained prompt (not the full
// accounting system instruction) asking the model to check its own entries.
func buildVerifyBalancePrompt(entriesJSON string) string {
	return fmt.Sprintf(`You are checking your own previous work for a simple arithmetic mistake.

Below are accounting journal entries you (or another instance of this model) generated earlier. Each entry has a debit or credit amount, and a "selection_reason"/"side_reason" text explaining how that amount was derived.

🎯 YOUR ONLY TASK: For each entry, check whether the "debit"/"credit" number matches what the reasoning text says it should be. Some entries' reasoning describes a calculation (e.g. "426.93 - 11.97 = 414.96") — verify the number in the field actually equals the result of that calculation. If it doesn't, correct ONLY that field's value.

🔴 STRICT RULES:
1. Do NOT change account_code, account_name, or which side (debit vs credit) any entry is on — only correct the numeric magnitude if it disagrees with the reasoning.
2. Do NOT add, remove, or reorder entries. Return exactly the same number of entries, in the same order.
3. Do NOT re-derive amounts from anything other than what the reasoning text itself already states — you are checking arithmetic consistency, not re-analyzing the document.
4. If every entry's number already matches its reasoning, return the entries completely unchanged.
5. Total debits should equal total credits once corrected — but only fix this by correcting a value the reasoning already justifies, never by inventing a new number to force balance.

ENTRIES TO CHECK:
%s

🎨 OUTPUT FORMAT — respond with ONLY a JSON array (no markdown fences, no explanation), same shape as the input:
[
  {"account_code": "...", "account_name": "...", "debit": 0, "credit": 0, "selection_reason": "...", "side_reason": "..."}
]
`, entriesJSON)
}

// VerifyAndCorrectBalance sends a narrow, focused verification prompt asking
// the model to cross-check its own selection_reason/side_reason text against
// the debit/credit values it wrote, and correct any mismatch. Follows the same
// retry/error-wrapping conventions as ProcessMultiImageAccountingAnalysis
// (gemini.go) so callers can handle it the same way.
func VerifyAndCorrectBalance(entries []VerifyEntry, reqCtx *common.RequestContext) ([]VerifyEntry, *common.TokenUsage, error) {
	entriesJSON, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal entries for verification: %w", err)
	}
	prompt := buildVerifyBalancePrompt(string(entriesJSON))

	ctx := context.Background()
	client, err := genai.NewClient(ctx,
		option.WithAPIKey(configs.GEMINI_API_KEY),
		option.WithEndpoint("https://generativelanguage.googleapis.com"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Gemini client for balance verification: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(configs.TEMPLATE_ACCOUNTING_MODEL_NAME)
	model.SetTemperature(0.0) // deterministic-as-possible for a pure consistency check

	reqCtx.LogInfo("🔎 Balance verification: sending %d entries to %s for self-check", len(entries), configs.TEMPLATE_ACCOUNTING_MODEL_NAME)

	var resp *genai.GenerateContentResponse
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ratelimit.WaitForRateLimit()

		resp, err = model.GenerateContent(ctx, genai.Text(prompt))
		if err == nil {
			break
		}

		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "resource exhausted") {
			if attempt < maxRetries {
				waitTime := time.Duration(attempt*10) * time.Second
				reqCtx.LogWarning("⚠️  Balance verification rate limit (429), waiting %v before retry (attempt %d/%d)", waitTime, attempt, maxRetries)
				time.Sleep(waitTime)
				continue
			}
		}
		break
	}

	if err != nil {
		return nil, nil, fmt.Errorf("balance verification failed after %d attempts: %w", maxRetries, err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, nil, fmt.Errorf("no response from Gemini during balance verification")
	}

	responseText := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	var corrected []VerifyEntry
	if err := json.Unmarshal([]byte(responseText), &corrected); err != nil {
		return nil, nil, fmt.Errorf("failed to parse balance verification response: %w", err)
	}

	var tokenUsage *common.TokenUsage
	if resp.UsageMetadata != nil {
		inputTokens := int(resp.UsageMetadata.PromptTokenCount)
		if resp.UsageMetadata.CachedContentTokenCount > 0 {
			inputTokens += int(resp.UsageMetadata.CachedContentTokenCount)
		}
		if resp.UsageMetadata.TotalTokenCount > (resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount) {
			thoughtTokens := resp.UsageMetadata.TotalTokenCount - resp.UsageMetadata.PromptTokenCount - resp.UsageMetadata.CandidatesTokenCount
			inputTokens += int(thoughtTokens)
		}
		tokens := common.CalculateTemplateAccountingTokenCost(inputTokens, int(resp.UsageMetadata.CandidatesTokenCount))
		reqCtx.LogInfo("🪙 Balance verification: %d input + %d output tokens", tokens.InputTokens, tokens.OutputTokens)
		tokenUsage = &tokens
	}

	return corrected, tokenUsage, nil
}
