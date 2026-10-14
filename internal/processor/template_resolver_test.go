package processor

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// The long multi-line promptdescription of the real INV-002 template (production, 2026-10-14).
const inv002Description = "ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ"
const inv002Prompt = "สกัดข้อมูลจากเอกสาร \"ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ\" (ชื่อหัวบิล: บริษัท ภูพิงค์แอร์เอนจิเนียริ่ง จำกัด) เพื่อบันทึกบัญชี\n\nข้อกำหนดการดึงยอดเงิน:\n- \"ยอดรวมทั้งสิ้น\" = ให้อ่านจากยอด \"รวมสุทธิ\" เท่านั้น\n\n1. บันทึกเข้า 03 สมุดรายวันรับ\n"

// What Gemini actually answered in production: the description plus only the first line of the promptdescription.
const aiEchoFromProdLog = "ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ | สกัดข้อมูลจากเอกสาร \"ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ\" (ชื่อหัวบิล: บริษัท ภูพิงค์แอร์เอนจิเนียริ่ง จำกัด) เพื่อบันทึกบัญชี"

// buildTemplateMap mirrors how AnalyzeTemplateMatch builds its lookup: bare description and "description | prompt".
func buildTemplateMap(templates []bson.M) map[string]bson.M {
	m := map[string]bson.M{}
	for _, t := range templates {
		description, _ := t["description"].(string)
		prompt, _ := t["promptdescription"].(string)
		combined := description
		if prompt != "" {
			combined = description + " | " + prompt
		}
		if description != "" {
			m[description] = t
		}
		m[combined] = t
	}
	return m
}

func sampleTemplates() []bson.M {
	return []bson.M{
		{"doccode": "INV-002", "description": inv002Description, "promptdescription": inv002Prompt},
		{"doccode": "INV-001", "description": "บิลเงินสด/ใบกำกับภาษีอย่างย่อ", "promptdescription": "บิลขายหน้าร้าน"},
		{"doccode": "RC-001", "description": "ใบเสร็จรับเงิน", "promptdescription": "รับเงินทั่วไป"},
		{"doccode": "PUR-001", "description": "ใบกำกับภาษีซื้อ", "promptdescription": ""},
	}
}

func TestResolveTemplateByAIName(t *testing.T) {
	m := buildTemplateMap(sampleTemplates())

	cases := []struct {
		name    string
		ai      string
		code    string // expected doccode, "" = not found
		wantHow string
	}{
		{"exact bare description", inv002Description, "INV-002", resolveExact},
		{"exact combined key", inv002Description + " | " + inv002Prompt, "INV-002", resolveExact},
		{"production echo: description + truncated prompt", aiEchoFromProdLog, "INV-002", resolveDescriptionKey},
		{"combined key with different whitespace layout", inv002Description + "  |  " + "สกัดข้อมูลจากเอกสาร \"ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ\" (ชื่อหัวบิล: บริษัท ภูพิงค์แอร์เอนจิเนียริ่ง จำกัด) เพื่อบันทึกบัญชี\n\nข้อกำหนดการดึงยอดเงิน:\n- \"ยอดรวมทั้งสิ้น\" = ให้อ่านจากยอด \"รวมสุทธิ\" เท่านั้น\n\n1. บันทึกเข้า 03 สมุดรายวันรับ\n", "INV-002", resolveNormalized},
		{"description followed by free text, no separator", inv002Description + " ตามที่ระบุ", "INV-002", resolvePrefix},
		{"surrounding spaces", "  " + inv002Description + "  ", "INV-002", resolveExact},
		{"shorter template is chosen when the AI named it", "ใบเสร็จรับเงิน | รับเงินทั่วไป", "RC-001", resolveExact},
		{"shorter template named with truncated context", "ใบเสร็จรับเงิน | รับเงิน", "RC-001", resolveDescriptionKey},
		{"unrelated text", "ใบแจ้งหนี้ค่าไฟฟ้า", "", ""},
		{"empty", "", "", ""},
		{"only the separator", " | ", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl, _, how, found := resolveTemplateByAIName(c.ai, m)
			if c.code == "" {
				if found {
					t.Fatalf("expected no match, got %v (%s)", tmpl["doccode"], how)
				}
				return
			}
			if !found {
				t.Fatalf("expected %s, got no match", c.code)
			}
			if tmpl["doccode"] != c.code {
				t.Fatalf("expected %s, got %v", c.code, tmpl["doccode"])
			}
			if c.wantHow != "" && how != c.wantHow {
				t.Fatalf("expected resolved by %q, got %q", c.wantHow, how)
			}
		})
	}
}

// A description that merely starts the same must not claim a longer template's text.
func TestResolveTemplateByAIName_PrefersLongestDescriptionAtBoundary(t *testing.T) {
	m := buildTemplateMap(sampleTemplates())

	tmpl, _, _, found := resolveTemplateByAIName("ใบเสร็จรับเงิน/ใบกำกับภาษีอย่างย่อ ของร้าน", m)
	if !found || tmpl["doccode"] != "INV-002" {
		t.Fatalf("expected INV-002 over the shorter 'ใบเสร็จรับเงิน', got %v found=%v", tmpl["doccode"], found)
	}

	// "ใบเสร็จรับเงินพิเศษ" shares a prefix with "ใบเสร็จรับเงิน" but is a different word → must not match RC-001
	if tmpl, _, _, found := resolveTemplateByAIName("ใบเสร็จรับเงินพิเศษ", m); found {
		t.Fatalf("a different name sharing only a prefix must not match, got %v", tmpl["doccode"])
	}
}

// Documents the original failure: the old fuzzy fallback could not recover this answer.
func TestProdEchoWasUnrecoverableByOldFuzzyMatching(t *testing.T) {
	m := buildTemplateMap(sampleTemplates())
	best := 0.0
	for key := range m {
		if s := calculateStringSimilarity(aiEchoFromProdLog, key); s > best {
			best = s
		}
	}
	if best > 0.75 {
		t.Fatalf("expected the old fuzzy similarity to stay below the 75%% threshold, got %.1f%%", best*100)
	}
}

func TestNormalizeTemplateName(t *testing.T) {
	if got := normalizeTemplateName("  a \n\n b\tc  "); got != "a b c" {
		t.Fatalf("unexpected normalization: %q", got)
	}
}
