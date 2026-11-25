package shopbrain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

func creditor(code, taxid string) *rawParty {
	return &rawParty{Code: code, TaxID: taxid, Names: []rawName{{Code: "th", Name: "ผู้ขาย " + code}}}
}

// purchase: Dr expense + Dr input VAT / Cr cash, with VAT 7%.
func purchase(guid string, d int, party *rawParty, expense string, base float64) Journal {
	vat := base * 0.07
	return Journal{
		GuidFixed: guid, DocNo: "PV" + guid, DocDate: day(d), CreatedAt: day(d), DocFormat: "PV", BookCode: "03",
		DebtAccountType: 1, Creditor: party, AccountDescription: "ซื้อของ " + guid + " 1,250.00",
		Details: []rawDetail{
			{AccountCode: expense, Debit: base}, {AccountCode: "11541", Debit: vat}, {AccountCode: "11111", Credit: base + vat},
		},
		Vats: []rawVat{{VatMode: 0, VatType: 0, VatRate: 7, VatBase: base, VatAmount: vat}},
	}
}

func fixture() (MasterData, []Journal, map[string]ImageGroup) {
	m := MasterData{
		Shop: ShopInfo{ShopID: "shopA", Name: "ร้านทดสอบ"},
		Accounts: []Account{
			{Code: "11111", Name: "เงินสด", Category: 1}, {Code: "11541", Name: "ภาษีซื้อ", Category: 1},
			{Code: "51212", Name: "ค่าขนส่ง", Category: 5}, {Code: "53000", Name: "ค่าใช้จ่ายเบ็ดเตล็ด", Category: 5},
			{Code: "99999", Name: "ไม่เคยใช้", Category: 5},
		},
		JournalBooks: []CodeName{{Code: "03", Name: "สมุดซื้อ"}, {Code: "06", Name: "สมุดเงินฝาก"}},
		Templates: []Template{{DocCode: "PV", Description: "ซื้อ", Lines: []TemplateLine{
			{AccountCode: "53000", Debit: "x"}, {AccountCode: "11541", Debit: "x"}, {AccountCode: "11111", Credit: "x"}}}},
		Creditors: []Party{{Kind: "creditor", Code: "C1", Name: "ผู้ขาย C1", TaxID: "0105500000001"}},
	}
	c1 := creditor("C1", "0105500000001")
	js := []Journal{
		purchase("j1", 1, c1, "51212", 100),
		purchase("j2", 2, c1, "51212", 200),
		purchase("j3", 3, c1, "51212", 300),
		// no counterparty, no image: learned through description / docformat
		{GuidFixed: "j4", DocNo: "JV001", DocDate: day(4), CreatedAt: day(4), DocFormat: "JV", BookCode: "01", AccountDescription: "ค่าไฟฟ้า 01/26",
			Details: []rawDetail{{AccountCode: "53000", Debit: 50}, {AccountCode: "11111", Credit: 50}}},
		{GuidFixed: "j5", DocNo: "JV002", DocDate: day(5), CreatedAt: day(5), DocFormat: "JV", BookCode: "01", AccountDescription: "ค่าไฟฟ้า 02/26",
			Details: []rawDetail{{AccountCode: "53000", Debit: 70}, {AccountCode: "11111", Credit: 70}}},
		{GuidFixed: "j6", DocNo: "JV003", DocDate: day(6), CreatedAt: day(6), DocFormat: "JV", BookCode: "01", AccountDescription: "ค่าไฟฟ้า 03/26",
			Details: []rawDetail{{AccountCode: "53000", Debit: 90}, {AccountCode: "11111", Credit: 90}}},
		// statement row that also has a documentref → must count as statement, not image
		{GuidFixed: "j7", DocNo: "BS001", DocDate: day(7), CreatedAt: day(7), BatchID: "batch1", DocumentRef: "g7", DocFormat: "BS", BookCode: "06",
			AccountDescription: "โอนเงิน", Details: []rawDetail{{AccountCode: "11111", Debit: 10}, {AccountCode: "40000", Credit: 10}}},
		// all-zero journal: nothing to learn
		{GuidFixed: "j8", DocNo: "Z1", DocDate: day(8), CreatedAt: day(8), Details: []rawDetail{{AccountCode: "11111"}}},
	}
	groups := map[string]ImageGroup{
		"g1": {GuidFixed: "g1", ImageReferences: []rawImageRef{{ImageURI: "https://img/1.jpg"}}},
		"g2": {GuidFixed: "g2", ImageReferences: []rawImageRef{{ImageURI: "https://img/2.jpg"}}},
		"g3": {GuidFixed: "g3"}, // group without images
		"g7": {GuidFixed: "g7", ImageReferences: []rawImageRef{{ImageURI: "https://img/7.pdf"}}},
	}
	js[0].DocumentRef, js[1].DocumentRef, js[2].DocumentRef = "g1", "g2", "g3"
	js[2].Vats[0].IsChecked = 1
	return m, js, groups
}

func build(t *testing.T) *Brain {
	t.Helper()
	m, js, g := fixture()
	opts := mechanicsOptions(day(10))
	return Build(m, js, g, 0, opts)
}

func findPattern(b *Brain, level Level, contains string) *Pattern {
	for _, p := range b.Patterns {
		if p.Level == level && strings.Contains(p.Key, contains) {
			return p
		}
	}
	return nil
}

func TestClassifySource(t *testing.T) {
	_, js, g := fixture()
	want := map[string]Source{"j1": SourceImage, "j2": SourceImage, "j3": SourceNoImage, "j4": SourceNoImage, "j7": SourceStatement}
	for _, j := range js {
		if w, ok := want[j.GuidFixed]; ok {
			if got, _ := classifySource(j, g); got != w {
				t.Errorf("%s: got %s want %s", j.GuidFixed, got, w)
			}
		}
	}
}

func TestReadinessCountsAllJournalsBySource(t *testing.T) {
	b := build(t)
	r := b.Readiness
	if r.Journals != 8 || r.BySource[SourceImage] != 2 || r.BySource[SourceNoImage] != 5 || r.BySource[SourceStatement] != 1 {
		t.Fatalf("unexpected readiness counts: %+v", r)
	}
	if !r.Insufficient {
		t.Errorf("8 journals must be flagged insufficient")
	}
}

func TestShapeSumsSameAccountAndDropsZeroLines(t *testing.T) {
	j := Journal{Details: []rawDetail{{AccountCode: "A", Debit: 40}, {AccountCode: "A", Debit: 60}, {AccountCode: "B", Credit: 100}, {AccountCode: "C"}}}
	s, lines, total := shapeOf(j)
	if s.Accounts != "AD,BC" || len(lines) != 2 || total != 100 {
		t.Fatalf("got %q %v %v", s.Accounts, lines, total)
	}
}

func TestPartyPatternLearnsShapeRatiosExamplesAndImages(t *testing.T) {
	b := build(t)
	p := findPattern(b, LevelTaxID, "0105500000001")
	if p == nil {
		t.Fatal("no L1 pattern for the creditor tax id")
	}
	if p.Total != 3 || p.Winner.Shape.Accounts != "11111C,11541D,51212D" || p.Consistency != 1 {
		t.Fatalf("unexpected pattern: total %d shape %s consistency %v", p.Total, p.Winner.Shape.Accounts, p.Consistency)
	}
	for _, l := range p.Winner.Lines {
		if l.AccountCode == "11541" && (l.RatioAvg < 0.065 || l.RatioAvg > 0.066) { // 7/107
			t.Errorf("input VAT ratio = %v, want ≈0.0654", l.RatioAvg)
		}
		if l.AccountCode == "51212" && l.AccountName != "ค่าขนส่ง" {
			t.Errorf("account name not taken from chart: %q", l.AccountName)
		}
	}
	if p.Winner.CheckedCount != 1 || len(p.Winner.Examples) != 2 || p.Winner.Examples[1].GuidFixed != "j3" {
		t.Errorf("checked/examples wrong: %+v", p.Winner)
	}
	if len(p.Winner.ExampleImages) != 2 {
		t.Errorf("want 2 example images (j1, j2), got %v", p.Winner.ExampleImages)
	}
	if p.Winner.Sources[SourceImage] != 2 || p.Winner.Sources[SourceNoImage] != 1 {
		t.Errorf("sources: %v", p.Winner.Sources)
	}
	if p.Winner.Shape.VAT == nil || p.Winner.Shape.VAT.VatRate != 7 {
		t.Errorf("VAT not learned: %+v", p.Winner.Shape.VAT)
	}
}

func TestNoPartyJournalsLearnedByDescriptionAndFormat(t *testing.T) {
	b := build(t)
	if p := findPattern(b, LevelDesc, "desc:ค่าไฟฟ้า|JV"); p == nil || p.Total != 3 || p.Winner.Shape.Accounts != "11111C,53000D" {
		t.Fatalf("description pattern missing or wrong: %+v", p)
	}
	if p := findPattern(b, LevelFormat, "fmt:JV|01"); p == nil || p.Total != 3 {
		t.Fatalf("format pattern missing: %+v", p)
	}
}

func TestMissingAccountWarningAndUsage(t *testing.T) {
	b := build(t)
	p := findPattern(b, LevelFormat, "fmt:BS|06")
	if p == nil || len(p.MissingAccts) != 1 || p.MissingAccts[0] != "40000" {
		t.Fatalf("expected missing account 40000, got %+v", p)
	}
	var never, outside bool
	for _, a := range b.Usage.Accounts {
		if a.Code == "99999" && a.Count == 0 && a.InChart {
			never = true
		}
		if a.Code == "40000" && !a.InChart && a.Count == 1 {
			outside = true
		}
	}
	if !never || !outside {
		t.Errorf("usage should list unused 99999 and out-of-chart 40000: %+v", b.Usage.Accounts)
	}
	if len(b.Usage.Templates) != 1 || b.Usage.Templates[0].JournalsUsing != 3 || b.Usage.Templates[0].MatchCount != 0 {
		t.Errorf("template PV allows 53000 but the shop books 51212: %+v", b.Usage.Templates)
	}
}

func TestBacktestUsesOnlyEarlierJournals(t *testing.T) {
	b := build(t)
	bt := b.Backtest
	// j1 cold; j2 by L1 (tax id needs 1 prior); j3 by L1; j4 cold; j5 cold (L3/L4 need 2); j6 by L3; j7 cold.
	if bt.Overall.Total != 7 || bt.Overall.Predicted != 3 || bt.Overall.HitAccounts != 3 || bt.Overall.HitFull != 3 {
		t.Fatalf("overall: %+v", bt.Overall)
	}
	if bt.ByLevel[LevelTaxID].Predicted != 2 || bt.ByLevel[LevelDesc].Predicted != 1 {
		t.Errorf("by level: L1 %+v L3 %+v", bt.ByLevel[LevelTaxID], bt.ByLevel[LevelDesc])
	}
	if bt.BaselinePartyDocformat.Total != 3 || bt.BaselinePartyDocformat.Predicted != 2 {
		t.Errorf("baseline: %+v", bt.BaselinePartyDocformat)
	}
}

func TestShapeChangeIsRecorded(t *testing.T) {
	m, _, g := fixture()
	c := creditor("C9", "")
	var js []Journal
	for i := 0; i < 2; i++ {
		js = append(js, purchase("a"+string(rune('0'+i)), i, c, "53000", 100))
	}
	for i := 0; i < 3; i++ {
		js = append(js, purchase("b"+string(rune('0'+i)), 10+i, c, "51212", 100))
	}
	b := Build(m, js, g, 0, mechanicsOptions(day(20)))
	p := findPattern(b, LevelParty, "party:creditor:C9|PV")
	if p == nil || len(p.Changes) != 1 || !strings.Contains(p.Changes[0].To, "51212D") || p.Winner.Shape.Accounts != "11111C,11541D,51212D" {
		t.Fatalf("expected one change to 51212: %+v", p)
	}
}

func TestCompareFindsAddedChangedRemovedAndEmptyWhenSame(t *testing.T) {
	m, js, g := fixture()
	prev := Build(m, js, g, 0, mechanicsOptions(day(10)))

	// Round-trip through JSON like the CLI does with -prev.
	data, _ := json.Marshal(prev)
	var prevRT Brain
	if err := json.Unmarshal(data, &prevRT); err != nil {
		t.Fatal(err)
	}
	same := Build(m, js, g, 0, mechanicsOptions(day(11)))
	if d := Compare(&prevRT, same); !d.Empty() {
		t.Fatalf("same data must give an empty diff: %+v", d.Totals)
	}

	edited := append([]Journal{}, js...)
	edited[0].Details = []rawDetail{{AccountCode: "53000", Debit: 107}, {AccountCode: "11111", Credit: 107}} // re-booked
	edited = edited[:len(edited)-1]                                                                          // j8 deleted
	edited = append(edited, purchase("j9", 9, creditor("C1", "0105500000001"), "51212", 10))
	d := Compare(&prevRT, Build(m, edited, g, 0, mechanicsOptions(day(12))))
	if d.Totals.Added != 1 || d.Totals.Changed != 1 || d.Totals.Removed != 1 || d.Added[0] != "j9" || d.Changed[0] != "j1" || d.Removed[0] != "j8" {
		t.Fatalf("diff: %+v", d)
	}
}

func TestVaultFilesAndNoSecrets(t *testing.T) {
	b := build(t)
	dir := t.TempDir()
	if err := WriteVault(b, dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"00-Index.md", "01-กิจการ.md", "02-ผังบัญชี.md", "03-สมุดและรูปแบบเอกสาร.md", "05-รูปแบบการลงบัญชี.md", "06-ผลจำลองย้อนหลัง.md", "04-คู่ค้า/คู่ค้า-เจ้าหนี้-C1.md"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
		if !strings.HasPrefix(string(data), "---\nstatus: from-data\n") {
			t.Errorf("%s has no frontmatter", f)
		}
	}
	party, _ := os.ReadFile(filepath.Join(dir, "04-คู่ค้า/คู่ค้า-เจ้าหนี้-C1.md"))
	if !strings.Contains(string(party), "https://img/1.jpg") || !strings.Contains(string(party), "[[02-ผังบัญชี]]") {
		t.Errorf("party note lacks example image or wikilink")
	}
	all, _ := json.Marshal(b)
	for _, bad := range []string{"apikey", "emailowners", "emailstaffs", "password", "phoneprimary"} {
		if strings.Contains(strings.ToLower(string(all)), bad) {
			t.Errorf("brain.json contains %q", bad)
		}
	}
}

func TestNormalizeDescriptionAndPrefix(t *testing.T) {
	if got := normalizeDescription("ค่าน้ำมัน 12/03 1,250.00 บาท"); got != "ค่าน้ำมัน บาท" {
		t.Errorf("normalize = %q", got)
	}
	if got := docnoPrefix("PV6903-0012"); got != "PV" {
		t.Errorf("prefix = %q", got)
	}
	if last4("123-4-56789-0") != "7890" {
		t.Errorf("last4")
	}
}

func TestLowConsistencyLevelIsSkippedInsteadOfGuessing(t *testing.T) {
	m, _, g := fixture()
	// Four party-less JV journals in book 01, each booked differently: L4 key "fmt:JV|01" is never consistent.
	var js []Journal
	for i, acct := range []string{"53000", "51212", "53000", "51212", "53000"} {
		js = append(js, Journal{GuidFixed: fmt.Sprint("x", i), DocNo: fmt.Sprint("JV", i), CreatedAt: day(i), DocDate: day(i), DocFormat: "JV", BookCode: "01",
			Details: []rawDetail{{AccountCode: acct, Debit: 10}, {AccountCode: "11111", Credit: 10}}})
	}
	strict := mechanicsOptions(day(10))
	b := Build(m, js, g, 0, strict)
	if b.Backtest.Overall.Predicted != 0 || b.Backtest.Skipped[LevelFormat] == 0 {
		t.Fatalf("inconsistent L4 must not predict: %+v skipped %v", b.Backtest.Overall, b.Backtest.Skipped)
	}
	if p := findPattern(b, LevelFormat, "fmt:JV|01"); p == nil || p.Usable {
		t.Fatalf("pattern should be marked not usable: %+v", p)
	}
	loose := strict
	loose.MinConsistencyFormat = 0
	if lb := Build(m, js, g, 0, loose); lb.Backtest.Overall.Predicted != 3 {
		t.Fatalf("with no bar every journal after the 2nd is guessed: %+v", lb.Backtest.Overall)
	}
}

func TestPlaceholderTaxIDIsIgnoredAndSharedTaxIDIsNotAttributed(t *testing.T) {
	for in, want := range map[string]string{"-": "", "0000000000000": "", "123": "", "0-1055-00000-01-1": "0105500000011", "0105500000011": "0105500000011", "ABC": ""} {
		if got := validTaxID(in); got != want {
			t.Errorf("validTaxID(%q) = %q, want %q", in, got, want)
		}
	}
	m, _, g := fixture()
	walkIn := []Journal{purchase("w1", 1, creditor("W1", "-"), "53000", 10), purchase("w2", 2, creditor("W2", "-"), "53000", 10)}
	shared := []Journal{purchase("s1", 3, creditor("S1", "0105500000011"), "53000", 10), purchase("s2", 4, creditor("S2", "0105500000011"), "53000", 10)}
	b := Build(m, append(walkIn, shared...), g, 0, mechanicsOptions(day(10)))
	if findPattern(b, LevelTaxID, "tax:-") != nil {
		t.Errorf("placeholder tax id must not form an L1 pattern")
	}
	p := findPattern(b, LevelTaxID, "tax:0105500000011")
	if p == nil || !p.MultiParty || p.PartyCode != "" {
		t.Fatalf("shared tax id should be multi-party: %+v", p)
	}
}
