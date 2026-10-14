// template_resolver.go - map the template name the AI returned back to a stored template
//
// The AI is shown each template as "description | promptdescription" and asked to answer with the
// matching template's name, but it is free text: with many templates, or a long multi-line
// promptdescription, the model often echoes only the description, or the description followed by a
// shortened/paraphrased promptdescription. An exact lookup then misses, and the word-overlap fuzzy
// fallback is useless for Thai (almost no spaces to tokenize on), so a correct, 100%-confidence AI
// choice was thrown away and the document fell back to "no template" mode (no VAT line flags, no
// actioncode enforcement, template confidence 0).
//
// resolveTemplateByAIName makes that lookup tolerant of how the AI echoes the name, while staying
// strict enough not to pick a template the AI did not choose.

package processor

import (
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// templateKeySeparator joins description and promptdescription in the keys built by AnalyzeTemplateMatch.
const templateKeySeparator = " | "

// how a template was resolved, for logging
const (
	resolveExact          = "exact"
	resolveNormalized     = "normalized"
	resolveDescriptionKey = "description"
	resolvePrefix         = "prefix"
)

// normalizeTemplateName collapses runs of whitespace (including newlines) so that the same text
// laid out differently still compares equal.
func normalizeTemplateName(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// resolveTemplateByAIName finds the template the AI meant.
//
// templateMap holds two kinds of keys per template: the bare description and the combined
// "description | promptdescription". Resolution order, stopping at the first hit:
//  1. exact key match
//  2. match ignoring whitespace layout
//  3. the text before the first " | " equals a description key
//  4. the AI text starts with a description key, at a word/separator boundary — the longest such
//     key wins, so "ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ" is preferred over a template named "ใบเสร็จรับเงิน"
//
// Returns the template, the key it matched and how; found is false when nothing matches.
func resolveTemplateByAIName(aiName string, templateMap map[string]bson.M) (template bson.M, key string, how string, found bool) {
	trimmed := strings.TrimSpace(aiName)
	if trimmed == "" {
		return nil, "", "", false
	}

	if t, ok := templateMap[aiName]; ok {
		return t, aiName, resolveExact, true
	}
	if t, ok := templateMap[trimmed]; ok {
		return t, trimmed, resolveExact, true
	}

	normalizedAI := normalizeTemplateName(trimmed)
	// iterate in a stable order so a tie (same normalized text under two keys) resolves the same way every run
	keys := make([]string, 0, len(templateMap))
	for k := range templateMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if normalizeTemplateName(k) == normalizedAI {
			return templateMap[k], k, resolveNormalized, true
		}
	}

	// the bare description keys are the ones without the combined-key separator
	descriptionKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		if !strings.Contains(k, templateKeySeparator) {
			descriptionKeys = append(descriptionKeys, k)
		}
	}

	if head, _, hasContext := strings.Cut(normalizedAI, strings.TrimSpace(templateKeySeparator)); hasContext {
		head = strings.TrimSpace(head)
		for _, k := range descriptionKeys {
			if normalizeTemplateName(k) == head {
				return templateMap[k], k, resolveDescriptionKey, true
			}
		}
	}

	bestKey := ""
	for _, k := range descriptionKeys {
		nk := normalizeTemplateName(k)
		if nk == "" || !strings.HasPrefix(normalizedAI, nk) {
			continue
		}
		// the description must end at a boundary, so "ใบเสร็จ" does not claim "ใบเสร็จรับเงิน..." text
		rest := normalizedAI[len(nk):]
		if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "|") {
			continue
		}
		if len(nk) > len(normalizeTemplateName(bestKey)) {
			bestKey = k
		}
	}
	if bestKey != "" {
		return templateMap[bestKey], bestKey, resolvePrefix, true
	}

	return nil, "", "", false
}
