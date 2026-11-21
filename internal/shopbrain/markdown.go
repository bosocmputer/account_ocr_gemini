package shopbrain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WriteVault writes the brain as an Obsidian-style vault: one folder per shop,
// numbered top-level notes, one note per counterparty, one "##" per pattern,
// frontmatter on every file and [[wikilinks]] between them.
func WriteVault(b *Brain, dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "04-คู่ค้า"), 0o755); err != nil {
		return err
	}
	for name, body := range renderVault(b) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// renderVault returns the vault as path (slash-separated) → content, for the
// CLI to write to disk and the export endpoint to zip.
func renderVault(b *Brain) map[string]string {
	w := &vault{b: b, acct: map[string]string{}}
	for _, a := range b.Master.Accounts {
		w.acct[a.Code] = a.Name
	}
	files := map[string]string{
		"00-Index.md":    w.index(),
		"01-กิจการ.md":   w.shop(),
		"02-ผังบัญชี.md": w.chart(),
		"03-สมุดและรูปแบบเอกสาร.md": w.books(),
		"05-รูปแบบการลงบัญชี.md":    w.generalPatterns(),
		"06-ผลจำลองย้อนหลัง.md":     w.backtest(),
	}
	for code, body := range w.parties() {
		files["04-คู่ค้า/"+partyNote(code)+".md"] = body
	}
	return files
}

type vault struct {
	b    *Brain
	acct map[string]string
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{M}\p{N}_-]+`)

// partyNote is the note name for "creditor:C001" → "คู่ค้า-เจ้าหนี้-C001" (unique across the vault).
func partyNote(kindCode string) string {
	kind, code, _ := strings.Cut(kindCode, ":")
	label := map[string]string{"creditor": "เจ้าหนี้", "debtor": "ลูกหนี้"}[kind]
	return "คู่ค้า-" + label + "-" + unsafeName.ReplaceAllString(code, "_")
}

func (w *vault) front(source string, tags ...string) string {
	t := append([]string{"generated", "shop-brain"}, tags...)
	return fmt.Sprintf("---\nstatus: from-data\ndate: %s\nsource: %q\nshopid: %s\ntags: [%s]\n---\n",
		w.b.GeneratedAt.Format("2006-01-02"), source, w.b.Master.Shop.ShopID, strings.Join(t, ", "))
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func esc(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s) }

func (w *vault) acctLabel(code string) string {
	if n := w.acct[code]; n != "" {
		return code + " " + n
	}
	return code + " (ไม่อยู่ในผังบัญชี)"
}

func (w *vault) index() string {
	b := w.b
	r := b.Readiness
	var s strings.Builder
	s.WriteString(w.front("bill_scan_project shopbrain v1", "index"))
	fmt.Fprintf(&s, "# สมองร้าน %s (Shop Brain)\n\n", esc(b.Master.Shop.Name))
	fmt.Fprintf(&s, "สร้างเมื่อ %s จาก journal %d รายการ (มีรูป %d, ไม่มีรูป %d, statement %d) ช่วง %s ถึง %s ใน %d เดือน\n\n",
		b.GeneratedAt.Format("2006-01-02 15:04"), r.Journals, r.BySource[SourceImage], r.BySource[SourceNoImage], r.BySource[SourceStatement],
		r.FirstEntry.Format("2006-01-02"), r.LastEntry.Format("2006-01-02"), r.ActiveMonths)
	if r.Insufficient {
		fmt.Fprintf(&s, "> [!warning] ข้อมูลยังไม่พอ: %s\n\n", strings.Join(r.Reasons, ", "))
	}
	fmt.Fprintf(&s, "## สารบัญ\n\n- [[01-กิจการ]] ตั้งค่ากิจการ สาขา งวดบัญชี\n- [[02-ผังบัญชี]] ผังบัญชี %d บัญชี และการใช้จริง\n- [[03-สมุดและรูปแบบเอกสาร]] สมุดรายวัน %d เล่ม และ template เดิม %d แบบ\n- คู่ค้า %d ราย ในโฟลเดอร์ `04-คู่ค้า`\n- [[05-รูปแบบการลงบัญชี]] รูปแบบที่ไม่ผูกกับคู่ค้า\n- [[06-ผลจำลองย้อนหลัง]] ความแม่นจากการจำลองย้อนหลัง\n\n",
		len(b.Master.Accounts), len(b.Master.JournalBooks), len(b.Master.Templates), len(b.Master.Creditors)+len(b.Master.Debtors))
	o := b.Backtest.Overall
	fmt.Fprintf(&s, "## สรุปความแม่น (จำลองย้อนหลัง)\n\nเดาได้ %d จาก %d รายการ (%s) บัญชีถูก %s, บัญชี+VAT+WHT ถูก %s\n",
		o.Predicted, o.Total, pct(o.Coverage), pct(o.RateAcc), pct(o.RateFull))
	if b.Diff != nil {
		t := b.Diff.Totals
		fmt.Fprintf(&s, "\n## เปลี่ยนจากรอบก่อน (%s)\n\njournal ใหม่ %d, ถูกแก้ %d, ถูกลบ %d • รูปแบบที่เปลี่ยน %d, ใหม่ %d, หายไป %d\n",
			b.Diff.PrevGeneratedAt.Format("2006-01-02 15:04"), t.Added, t.Changed, t.Removed, t.PatternsChanged, t.PatternsNew, t.PatternsGone)
	}
	return s.String()
}

func optInt(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func (w *vault) shop() string {
	m := w.b.Master
	sh := m.Shop
	var s strings.Builder
	s.WriteString(w.front("collection shops, organizationBranches, organizationBusinessTypes, organizationDepartments, accountPeriodMaster, accountPeriodClosed", "shop"))
	fmt.Fprintf(&s, "# กิจการ (Shop Profile) %s\n\n## ตั้งค่ากิจการ (Settings)\n\n", esc(sh.Name))
	fmt.Fprintf(&s, "| รายการ | ค่า |\n|---|---|\n| shopid | %s |\n| เลขผู้เสียภาษี (Tax ID) | %s |\n| รหัสสาขา | %s |\n| ประเภท VAT ขาย / ซื้อ | %s / %s |\n| inquiry ขาย / ซื้อ | %s / %s |\n| ใช้สาขา / ใช้แผนก | %t / %t |\n| ใช้ปี พ.ศ. | %t |\n| สกุลเงิน | %s |\n\n",
		sh.ShopID, sh.TaxID, sh.BranchCode, optInt(sh.VatTypeSale), optInt(sh.VatTypePurchase), optInt(sh.InquiryTypeSale), optInt(sh.InquiryTypePurchase),
		sh.IsUseBranch, sh.IsUseDepartment, sh.UseBuddhistCalendar, sh.BaseCurrency)
	if strings.TrimSpace(sh.PromptShopInfo) != "" {
		fmt.Fprintf(&s, "## คำอธิบายธุรกิจ (promptshopinfo)\n\n%s\n\n", sh.PromptShopInfo)
	}
	list := func(title string, rows []CodeName) {
		fmt.Fprintf(&s, "## %s\n\n", title)
		if len(rows) == 0 {
			s.WriteString("ไม่มีข้อมูล\n\n")
			return
		}
		for _, r := range rows {
			fmt.Fprintf(&s, "- `%s` %s\n", r.Code, esc(r.Name))
		}
		s.WriteString("\n")
	}
	list("สาขา (Branches)", m.Branches)
	list("แผนก (Departments)", m.Departments)
	var bts []CodeName
	for _, b := range m.BusinessTypes {
		bts = append(bts, CodeName{Code: b.Code, Name: b.Name})
	}
	list("ประเภทธุรกิจ (Business Types)", bts)
	s.WriteString("## งวดบัญชี (Account Periods)\n\n")
	if len(m.Periods) == 0 {
		s.WriteString("ไม่มีข้อมูล\n\n")
	} else {
		s.WriteString("| งวด | เริ่ม | สิ้นสุด | ปิดใช้ |\n|---|---|---|---|\n")
		for _, p := range m.Periods {
			fmt.Fprintf(&s, "| %d | %s | %s | %t |\n", p.Period, p.StartDate.Format("2006-01-02"), p.EndDate.Format("2006-01-02"), p.IsDisabled)
		}
		s.WriteString("\n")
	}
	s.WriteString("## งวดที่ปิดแล้ว (Closed Periods)\n\n")
	if len(m.ClosedPeriods) == 0 {
		s.WriteString("ไม่มีข้อมูล\n")
	}
	for _, c := range m.ClosedPeriods {
		fmt.Fprintf(&s, "- %d/%02d\n", c.AccountYear, c.AccountMonth)
	}
	if len(m.BookBanks) > 0 || len(m.Banks) > 0 {
		s.WriteString("\n## สมุดบัญชีธนาคาร (Book Bank)\n\n")
		for _, b := range m.BookBanks {
			fmt.Fprintf(&s, "- %s %s เลขบัญชีลงท้าย %s\n", esc(b.BankName), esc(b.Name), b.PassbookLast4)
		}
	}
	return s.String()
}

var categoryName = map[int]string{1: "สินทรัพย์ (Assets)", 2: "หนี้สิน (Liabilities)", 3: "ส่วนของเจ้าของ (Equity)", 4: "รายได้ (Revenue)", 5: "ค่าใช้จ่าย (Expenses)"}

func (w *vault) chart() string {
	var s strings.Builder
	s.WriteString(w.front("collection chartofaccounts + journals", "chart-of-accounts"))
	s.WriteString("# ผังบัญชี (Chart of Accounts, COA)\n\nจำนวนครั้ง = จำนวนบรรทัดบัญชีใน journal ที่ใช้รหัสนี้\n\n")
	cat := map[string]int{}
	for _, a := range w.b.Master.Accounts {
		cat[a.Code] = a.Category
	}
	groups := map[int][]AccountUsage{}
	for _, u := range w.b.Usage.Accounts {
		c := cat[u.Code]
		if !u.InChart {
			c = -1
		}
		groups[c] = append(groups[c], u)
	}
	keys := make([]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		title := categoryName[k]
		switch {
		case k == -1:
			title = "ใช้ใน journal แต่ไม่มีในผังบัญชีปัจจุบัน"
		case title == "":
			title = fmt.Sprintf("หมวด %d", k)
		}
		fmt.Fprintf(&s, "## %s\n\n| รหัส | ชื่อบัญชี | ใช้ (ครั้ง) | ใช้ล่าสุด |\n|---|---|---|---|\n", title)
		for _, u := range groups[k] {
			last := "ไม่เคยใช้"
			if u.Count > 0 {
				last = u.LastUsed.Format("2006-01-02")
			}
			fmt.Fprintf(&s, "| %s | %s | %d | %s |\n", u.Code, esc(u.Name), u.Count, last)
		}
		s.WriteString("\n")
	}
	return s.String()
}

func (w *vault) books() string {
	u := w.b.Usage
	var s strings.Builder
	s.WriteString(w.front("collection journalBooks, documentFormate + journals", "journal-books", "templates"))
	s.WriteString("# สมุดรายวันและรูปแบบเอกสาร (Journal Books, Document Formats)\n\n## สมุดรายวัน (Journal Books)\n\n| รหัส | ชื่อ | ใช้ (ครั้ง) |\n|---|---|---|\n")
	used := map[string]int{}
	for _, b := range u.Books {
		used[b.Code] = b.Count
	}
	for _, b := range w.b.Master.JournalBooks {
		fmt.Fprintf(&s, "| %s | %s | %d |\n", b.Code, esc(b.Name), used[b.Code])
	}
	s.WriteString("\n## ประเภทเอกสารที่ใช้จริง (docformat)\n\n| docformat | ชื่อ | ใช้ (ครั้ง) |\n|---|---|---|\n")
	for _, f := range u.DocFormat {
		fmt.Fprintf(&s, "| %s | %s | %d |\n", f.Code, esc(f.Name), f.Count)
	}
	s.WriteString("\n## Template เดิม เทียบกับการลงบัญชีจริง\n\nตรงกับ template = ทุกบัญชีใน journal อยู่ในบัญชีที่ template ระบุ\n\n")
	if len(u.Templates) == 0 {
		s.WriteString("ร้านนี้ไม่มี template\n")
	}
	tpl := map[string]Template{}
	for _, t := range w.b.Master.Templates {
		tpl[t.DocCode] = t
	}
	for _, t := range u.Templates {
		fmt.Fprintf(&s, "### %s %s\n\n", t.DocCode, esc(t.Description))
		for _, l := range tpl[t.DocCode].Lines {
			fmt.Fprintf(&s, "- %s Dr:%s Cr:%s %s\n", firstNonEmpty(l.AccountCode, l.DebitAccount, l.CreditAccount), firstNonEmpty(l.Debit, "-"), firstNonEmpty(l.Credit, "-"), esc(l.Detail))
		}
		if t.JournalsUsing == 0 {
			s.WriteString("\nไม่มี journal ที่ใช้ docformat นี้\n\n")
			continue
		}
		fmt.Fprintf(&s, "\nใช้ใน journal %d รายการ ตรงกับ template %d (%s) • โครงที่ใช้จริงบ่อยสุด `%s`\n\n", t.JournalsUsing, t.MatchCount, pct(t.MatchRate), t.TopActual)
	}
	return s.String()
}

func (w *vault) variant(s *strings.Builder, v *Variant, label string) {
	fmt.Fprintf(s, "**%s** ใช้ %d ครั้ง (ตรวจแล้ว %d) ล่าสุด %s\n\n", label, v.Count, v.CheckedCount, v.LastSeen.Format("2006-01-02"))
	s.WriteString("| ฝั่ง | บัญชี | สัดส่วนของยอดรวม (เฉลี่ย / ต่ำสุด–สูงสุด) |\n|---|---|---|\n")
	for _, l := range v.Lines {
		side := "Dr"
		if l.Side == "C" {
			side = "Cr"
		}
		fmt.Fprintf(s, "| %s | %s | %.4f / %.4f–%.4f |\n", side, esc(w.acctLabel(l.AccountCode)), l.RatioAvg, l.RatioMin, l.RatioMax)
	}
	if v.Shape.VAT != nil {
		fmt.Fprintf(s, "\nVAT: vatmode %d, vattype %d, อัตรา %s%%", v.Shape.VAT.VatMode, v.Shape.VAT.VatType, fnum(v.Shape.VAT.VatRate))
	} else {
		s.WriteString("\nVAT: ไม่มี")
	}
	if v.Shape.WHT != nil {
		fmt.Fprintf(s, " • WHT: taxtype %d, conditiontaxtype %d, อัตรา %s%% %s", v.Shape.WHT.TaxType, v.Shape.WHT.ConditionTaxType, fnum(v.Shape.WHT.TaxRate), esc(v.Shape.WHT.IncomeType))
	} else {
		s.WriteString(" • WHT: ไม่มี")
	}
	s.WriteString("\n\n")
	fmt.Fprintf(s, "สมุด %s • prefix เลขเอกสาร %s • ที่มา %s\n\n", topKeys(v.BookCodes), topKeys(v.DocNoPrefixes), sourceMix(v.Sources))
	if len(v.Examples) > 0 {
		var ex []string
		for _, e := range v.Examples {
			ex = append(ex, fmt.Sprintf("%s (%s)", e.DocNo, e.DocDate.Format("2006-01-02")))
		}
		fmt.Fprintf(s, "ตัวอย่าง: %s\n\n", strings.Join(ex, ", "))
	}
	for _, img := range v.ExampleImages {
		fmt.Fprintf(s, "- รูปตัวอย่าง: %s\n", img)
	}
	if len(v.ExampleImages) > 0 {
		s.WriteString("\n")
	}
}

func topKeys(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var l []kv
	for k, v := range m {
		if k == "" {
			k = "(ว่าง)"
		}
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(a, b int) bool { return l[a].v > l[b].v || (l[a].v == l[b].v && l[a].k < l[b].k) })
	var out []string
	for i, x := range l {
		if i == 3 {
			break
		}
		out = append(out, fmt.Sprintf("`%s`×%d", x.k, x.v))
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, " ")
}

func sourceMix(m map[Source]int) string {
	label := map[Source]string{SourceImage: "มีรูป", SourceNoImage: "ไม่มีรูป", SourceStatement: "statement"}
	var out []string
	for _, src := range allSources {
		if m[src] > 0 {
			out = append(out, fmt.Sprintf("%s %d", label[src], m[src]))
		}
	}
	return strings.Join(out, ", ")
}

func (w *vault) pattern(s *strings.Builder, p *Pattern, title string) {
	fmt.Fprintf(s, "## %s %s\n\n", p.ID, title)
	usable := "✅ ใช้เดาได้"
	if !p.Usable {
		usable = fmt.Sprintf("⚠️ ยังไม่ใช้เดา (ต้องพบ ≥ %d ครั้ง และสม่ำเสมอ ≥ %s)", minSupport(p.Level), pct(w.b.Options.minConsistency(p.Level)))
	}
	fmt.Fprintf(s, "ระดับ `%s` • docformat `%s` • พบ %d ครั้ง • ความสม่ำเสมอ %s • %s\n\n", p.Level, firstNonEmpty(p.DocFormat, "-"), p.Total, pct(p.Consistency), usable)
	if len(p.MissingAccts) > 0 {
		fmt.Fprintf(s, "> [!warning] บัญชีต่อไปนี้ไม่มีในผังบัญชีปัจจุบันแล้ว: %s\n\n", strings.Join(p.MissingAccts, ", "))
	}
	w.variant(s, p.Winner, "รูปแบบหลัก")
	for i, o := range p.Others {
		w.variant(s, o, fmt.Sprintf("รูปแบบรอง %d", i+1))
	}
	for _, c := range p.Changes {
		fmt.Fprintf(s, "- เปลี่ยนวิธีลงเมื่อ %s: `%s` → `%s`\n", c.At.Format("2006-01-02"), c.From, c.To)
	}
	if len(p.Changes) > 0 {
		s.WriteString("\n")
	}
	s.WriteString("ดู [[02-ผังบัญชี]] • [[03-สมุดและรูปแบบเอกสาร]]\n\n")
}

func (w *vault) parties() map[string]string {
	byParty := map[string][]*Pattern{}
	for _, p := range w.b.Patterns {
		if (p.Level == LevelTaxID || p.Level == LevelParty) && p.PartyCode != "" {
			k := p.PartyKind + ":" + p.PartyCode
			byParty[k] = append(byParty[k], p)
		}
	}
	info := map[string]Party{}
	for _, p := range append(append([]Party{}, w.b.Master.Creditors...), w.b.Master.Debtors...) {
		info[p.Kind+":"+p.Code] = p
	}
	out := map[string]string{}
	for k, ps := range byParty {
		pi := info[k]
		name := firstNonEmpty(pi.Name, ps[0].PartyName)
		var s strings.Builder
		s.WriteString(w.front("collection creditors/debtors + journals", "counterparty", pi.Kind))
		kindTH := map[string]string{"creditor": "เจ้าหนี้ (Creditor, AP)", "debtor": "ลูกหนี้ (Debtor, AR)"}[ps[0].PartyKind]
		fmt.Fprintf(&s, "# %s %s\n\n%s รหัส `%s` • เลขผู้เสียภาษี `%s` • สาขา `%s` • บัญชีคู่ค้า %s\n\n",
			esc(name), kindTH, kindTH, ps[0].PartyCode, firstNonEmpty(pi.TaxID, ps[0].TaxID, "-"), firstNonEmpty(pi.BranchNumber, "-"), firstNonEmpty(pi.AccountCode, "-"))
		for _, p := range ps {
			w.pattern(&s, p, fmt.Sprintf("%s (%s)", firstNonEmpty(p.DocFormat, "ไม่ระบุ docformat"), p.Level))
		}
		s.WriteString("กลับ [[00-Index]]\n")
		out[k] = s.String()
	}
	return out
}

func (w *vault) generalPatterns() string {
	var s strings.Builder
	s.WriteString(w.front("journals (L3 description, L4 docformat+book)", "patterns"))
	s.WriteString("# รูปแบบการลงบัญชีที่ไม่ผูกกับคู่ค้า (Booking Patterns)\n\nใช้กับรายการที่ไม่มีคู่ค้า หรือคู่ค้าที่ยังไม่เคยเจอ ระดับ L3 จับจากคำอธิบายรายการ ระดับ L4 จับจากประเภทเอกสาร + สมุด\n\n")
	for _, p := range w.b.Patterns {
		switch p.Level {
		case LevelTaxID:
			if p.MultiParty {
				w.pattern(&s, p, fmt.Sprintf("เลขผู้เสียภาษี %s ใช้ร่วมหลายรหัสคู่ค้า (%s)", p.TaxID, firstNonEmpty(p.DocFormat, "-")))
			}
		case LevelDesc:
			w.pattern(&s, p, fmt.Sprintf("คำอธิบาย \"%s\" (%s)", esc(p.Description), firstNonEmpty(p.DocFormat, "-")))
		case LevelFormat:
			w.pattern(&s, p, fmt.Sprintf("docformat %s สมุด %s", firstNonEmpty(p.DocFormat, "-"), firstNonEmpty(p.BookCode, "-")))
		}
	}
	return s.String()
}

func (w *vault) backtest() string {
	bt := w.b.Backtest
	var s strings.Builder
	s.WriteString(w.front("backtest over journals in createdat order", "backtest"))
	s.WriteString("# ผลจำลองย้อนหลัง (Backtest)\n\nเรียง journal ตามเวลาที่บันทึก ใช้เฉพาะรายการก่อนหน้าเดารายการถัดไป แล้วเทียบกับที่บันทึกจริง (นับแบบไม่ถ่วงน้ำหนัก)\n\n")
	row := func(name string, h HitStat) string {
		return fmt.Sprintf("| %s | %d | %d | %s | %s | %s |\n", name, h.Total, h.Predicted, pct(h.Coverage), pct(h.RateAcc), pct(h.RateFull))
	}
	head := "| กลุ่ม | ทั้งหมด | เดาได้ | coverage | บัญชีถูก | บัญชี+VAT+WHT ถูก |\n|---|---|---|---|---|---|\n"
	s.WriteString("## ภาพรวม\n\n" + head + row("ทั้งหมด", bt.Overall))
	s.WriteString("\n## แยกตามที่มา\n\n" + head)
	for _, src := range allSources {
		s.WriteString(row(string(src), *bt.BySource[src]))
	}
	s.WriteString("\n## แยกตามระดับตัวจับที่ใช้เดา\n\n" + head)
	for _, l := range allLevels {
		s.WriteString(row(string(l), *bt.ByLevel[l]))
	}
	s.WriteString("\n## เทียบกับวิธีเดิม (คู่ค้า + docformat อย่างเดียว)\n\n" + head + row("baseline", bt.BaselinePartyDocformat))
	return s.String()
}
