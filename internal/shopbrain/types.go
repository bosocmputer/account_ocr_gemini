// Package shopbrain builds a per-shop "brain": the shop's master data plus
// the booking patterns learned from the journals it has already saved, so
// the OCR pipeline (and anything else) can follow how this particular shop
// books a document instead of relying on hand-written templates.
//
// v1 is read-only: it loads with find({shopid}) + whitelisted projections,
// builds everything in memory and only writes files through the CLI in
// cmd/shopbrain. Nothing here writes to MongoDB.
package shopbrain

import "time"

// Source says where a journal came from. Learning uses the same features
// for every source; an image journal just also carries example images.
type Source string

const (
	SourceStatement Source = "statement" // batchid set (bank-statement import)
	SourceImage     Source = "image"     // documentref → a group that has images
	SourceNoImage   Source = "no_image"  // keyed by hand / Excel import / group missing or empty
)

var allSources = []Source{SourceImage, SourceNoImage, SourceStatement}

// Level is the key-ladder level used to look a journal up.
type Level string

const (
	LevelTaxID   Level = "L1_taxid"       // counterparty tax ID + docformat
	LevelParty   Level = "L2_party"       // counterparty code + docformat
	LevelDesc    Level = "L3_description" // normalized accountdescription + docformat
	LevelFormat  Level = "L4_format"      // docformat + bookcode
	levelNoMatch Level = "none"
)

var allLevels = []Level{LevelTaxID, LevelParty, LevelDesc, LevelFormat}

// ---------- raw records decoded from Mongo (only whitelisted fields) ----------

type rawName struct {
	Code     string `bson:"code" json:"code"`
	Name     string `bson:"name" json:"name"`
	IsDelete bool   `bson:"isdelete" json:"-"`
}

type rawParty struct {
	Code         string    `bson:"code"`
	TaxID        string    `bson:"taxid"`
	BranchNumber string    `bson:"branchnumber"`
	Names        []rawName `bson:"names"`
}

type rawDetail struct {
	AccountCode string  `bson:"accountcode"`
	AccountName string  `bson:"accountname"`
	Debit       float64 `bson:"debitamount"`
	Credit      float64 `bson:"creditamount"`
}

type rawVat struct {
	VatMode   int     `bson:"vatmode"`
	VatType   int     `bson:"vattype"`
	VatRate   float64 `bson:"vatrate"`
	VatBase   float64 `bson:"vatbase"`
	VatAmount float64 `bson:"vatamount"`
	ExceptVat float64 `bson:"exceptvat"`
	CustTaxID string  `bson:"custtaxid"`
	CustName  string  `bson:"custname"`
	IsChecked int     `bson:"ischecked"`
}

type rawTaxDetail struct {
	TaxBase     float64 `bson:"taxbase"`
	TaxRate     float64 `bson:"taxrate"`
	TaxAmount   float64 `bson:"taxamount"`
	Description string  `bson:"description"`
}

type rawTax struct {
	TaxType          int            `bson:"taxtype"`
	ConditionTaxType int            `bson:"conditiontaxtype"`
	CustType         int            `bson:"custtype"`
	CustTaxID        string         `bson:"custtaxid"`
	TaxAmount        float64        `bson:"taxamount"`
	IsChecked        int            `bson:"ischecked"`
	Details          []rawTaxDetail `bson:"details"`
}

// Journal is one saved GL entry, reduced to what the brain learns from.
type Journal struct {
	GuidFixed          string      `bson:"guidfixed"`
	DocNo              string      `bson:"docno"`
	DocDate            time.Time   `bson:"docdate"`
	DocumentRef        string      `bson:"documentref"`
	BatchID            string      `bson:"batchid"`
	DocFormat          string      `bson:"docformat"`
	BookCode           string      `bson:"bookcode"`
	BranchCode         string      `bson:"branchcode"`
	AccountDescription string      `bson:"accountdescription"`
	DebtAccountType    int         `bson:"debtaccounttype"`
	Creditor           *rawParty   `bson:"creditor"`
	Debtor             *rawParty   `bson:"debtor"`
	Details            []rawDetail `bson:"journaldetail"`
	Vats               []rawVat    `bson:"vats"`
	Taxes              []rawTax    `bson:"taxes"`
	CreatedAt          time.Time   `bson:"createdat"`
	UpdatedAt          time.Time   `bson:"updatedat"`
}

type rawImageRef struct {
	ImageURI          string `bson:"imageuri"`
	DocumentImageGUID string `bson:"documentimageguid"`
	Name              string `bson:"name"`
}

// ImageGroup is a documentImageGroups row (the set of images behind a journal).
type ImageGroup struct {
	GuidFixed       string        `bson:"guidfixed"`
	Status          int           `bson:"status"`
	ImageReferences []rawImageRef `bson:"imagereferences"`
}

// ---------- master data (output, whitelisted) ----------

type ShopInfo struct {
	ShopID              string    `json:"shopid"`
	Name                string    `json:"name"`
	Names               []rawName `json:"names"`
	TaxID               string    `json:"taxid"`
	BranchCode          string    `json:"branchcode"`
	PromptShopInfo      string    `json:"promptshopinfo"`
	VatTypeSale         *int      `json:"vattypesale"`
	VatTypePurchase     *int      `json:"vattypepurchase"`
	InquiryTypeSale     *int      `json:"inquirytypesale"`
	InquiryTypePurchase *int      `json:"inquirytypepurchase"`
	IsUseBranch         bool      `json:"isusebranch"`
	IsUseDepartment     bool      `json:"isusedepartment"`
	UseBuddhistCalendar bool      `json:"usebuddhistcalendar"`
	BaseCurrency        string    `json:"base_currency"`
}

type Account struct {
	Code                   string `bson:"accountcode" json:"code"`
	Name                   string `bson:"accountname" json:"name"`
	Category               int    `bson:"accountcategory" json:"category"`
	BalanceType            int    `bson:"accountbalancetype" json:"balancetype"`
	FinancialStatements    int    `bson:"financialstatements" json:"financialstatements"`
	Group                  string `bson:"accountgroup" json:"group"`
	Level                  int    `bson:"accountlevel" json:"level"`
	ConsolidateAccountCode string `bson:"consolidateaccountcode" json:"consolidateaccountcode"`
}

type CodeName struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type TemplateLine struct {
	AccountCode        string `json:"accountcode"`
	Detail             string `json:"detail"`
	Debit              string `json:"debit"`
	Credit             string `json:"credit"`
	DebitAccount       string `json:"debitaccount"`
	CreditAccount      string `json:"creditaccount"`
	IsVatLine          bool   `json:"isvatline"`
	LinkToDebtAccount  bool   `json:"linktodebtaccount"`
	IsEntrySelfAccount bool   `json:"isentryselfaccount"`
}

type Template struct {
	DocCode           string         `json:"doccode"`
	DocFormat         string         `json:"docformat"`
	Description       string         `json:"description"`
	Module            string         `json:"module"`
	BookCode          string         `json:"bookcode"`
	AccountGroup      string         `json:"accountgroup"`
	IsAutoFormat      bool           `json:"isautoformat"`
	PromptDescription string         `json:"promptdescription"`
	Lines             []TemplateLine `json:"lines"`
}

type BusinessType struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isdefault"`
}

type Period struct {
	Period      int       `bson:"period" json:"period"`
	StartDate   time.Time `bson:"startdate" json:"startdate"`
	EndDate     time.Time `bson:"enddate" json:"enddate"`
	Description string    `bson:"description" json:"description"`
	IsDisabled  bool      `bson:"isdisabled" json:"isdisabled"`
}

type ClosedPeriod struct {
	AccountYear  int `bson:"accountyear" json:"accountyear"`
	AccountMonth int `bson:"accountmonth" json:"accountmonth"`
}

// Party is a creditor or debtor without address, phone, e-mail or login data.
type Party struct {
	Kind         string `json:"kind"` // creditor | debtor
	Code         string `json:"code"`
	Name         string `json:"name"`
	TaxID        string `json:"taxid"`
	BranchNumber string `json:"branchnumber"`
	PersonalType int    `json:"personaltype"`
	CustomerType int    `json:"customertype"`
	AccountCode  string `json:"accountcode"`
	AccountName  string `json:"accountname"`
	CreditDay    int    `json:"creditday"`
}

type MasterItem struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	AccountCode string `json:"accountcode"`
	AccountName string `json:"accountname"`
}

type BookBank struct {
	BankCode      string `json:"bankcode"`
	Name          string `json:"name"`
	BankName      string `json:"bankname"`
	PassbookLast4 string `json:"passbook_last4"` // masked: only the last 4 digits leave the DB
}

type MasterData struct {
	Shop           ShopInfo       `json:"shop"`
	Accounts       []Account      `json:"chartofaccounts"`
	JournalBooks   []CodeName     `json:"journalbooks"`
	Templates      []Template     `json:"documentformats"`
	AccountGroups  []CodeName     `json:"accountgroups"`
	Branches       []CodeName     `json:"branches"`
	Departments    []CodeName     `json:"departments"`
	BusinessTypes  []BusinessType `json:"businesstypes"`
	Periods        []Period       `json:"accountperiods"`
	ClosedPeriods  []ClosedPeriod `json:"closedperiods"`
	Creditors      []Party        `json:"creditors"`
	Debtors        []Party        `json:"debtors"`
	MasterIncomes  []MasterItem   `json:"masterincomes"`
	MasterExpenses []MasterItem   `json:"masterexpenses"`
	BookBanks      []BookBank     `json:"bookbanks"`
	Banks          []CodeName     `json:"banks"`
}

// ---------- learned knowledge ----------

// ShapeLine is one account/side of a booking shape with its share of the total.
type ShapeLine struct {
	AccountCode string  `json:"accountcode"`
	AccountName string  `json:"accountname"`
	Side        string  `json:"side"`      // D | C
	RatioAvg    float64 `json:"ratio_avg"` // line amount ÷ document total (sum of debits)
	RatioMin    float64 `json:"ratio_min"`
	RatioMax    float64 `json:"ratio_max"`
	ratioSum    float64
	ratioN      int
}

type VatInfo struct {
	VatMode int     `json:"vatmode"`
	VatType int     `json:"vattype"`
	VatRate float64 `json:"vatrate"`
}

type WhtInfo struct {
	TaxType          int     `json:"taxtype"`
	ConditionTaxType int     `json:"conditiontaxtype"`
	TaxRate          float64 `json:"taxrate"`
	IncomeType       string  `json:"incometype"` // details[0].description
}

// Shape is how one journal was booked.
type Shape struct {
	Accounts string   `json:"accounts"` // "11111C,51212D"
	VAT      *VatInfo `json:"vat,omitempty"`
	WHT      *WhtInfo `json:"wht,omitempty"`
}

func (s Shape) fullKey() string { return s.Accounts + "|" + vatKey(s.VAT) + "|" + whtKey(s.WHT) }

type Example struct {
	DocNo     string    `json:"docno"`
	GuidFixed string    `json:"guidfixed"`
	DocDate   time.Time `json:"docdate"`
	Source    Source    `json:"source"`
}

type ShapeChange struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
}

// Variant is one booking shape seen under a key.
type Variant struct {
	Shape         Shape          `json:"shape"`
	Lines         []*ShapeLine   `json:"lines"`
	Count         int            `json:"count"`
	Weight        float64        `json:"weight"`
	CheckedCount  int            `json:"checked_count"`
	FirstSeen     time.Time      `json:"first_seen"`
	LastSeen      time.Time      `json:"last_seen"`
	Sources       map[Source]int `json:"sources"`
	BookCodes     map[string]int `json:"bookcodes"`
	DocNoPrefixes map[string]int `json:"docno_prefixes"`
	BranchCodes   map[string]int `json:"branchcodes,omitempty"`
	Examples      []Example      `json:"examples"`
	ExampleImages []string       `json:"example_images,omitempty"`
}

// Pattern is everything learned under one key of the ladder.
type Pattern struct {
	ID           string        `json:"id"` // PAT-xxxx, stable for the same key
	Level        Level         `json:"level"`
	Key          string        `json:"key"`
	DocFormat    string        `json:"docformat"`
	PartyKind    string        `json:"party_kind,omitempty"`
	PartyCode    string        `json:"party_code,omitempty"`
	PartyName    string        `json:"party_name,omitempty"`
	TaxID        string        `json:"taxid,omitempty"`
	MultiParty   bool          `json:"multi_party,omitempty"` // tax ID shared by several counterparty codes
	Description  string        `json:"description,omitempty"`
	BookCode     string        `json:"bookcode,omitempty"`
	Direction    string        `json:"direction,omitempty"` // bank statement only: "in" / "out"
	Total        int           `json:"total"`
	Consistency  float64       `json:"consistency"` // winner weight ÷ total weight
	Usable       bool          `json:"usable"`      // passes the same bars as the predictor (support, consistency, Wilson, recent agreement)
	Winner       *Variant      `json:"winner"`
	Others       []*Variant    `json:"others,omitempty"`
	Changes      []ShapeChange `json:"changes,omitempty"`
	MissingAccts []string      `json:"missing_accounts,omitempty"` // in winner but not in the current chart of accounts
	variants     map[string]*Variant
	countsSoFar  map[string]int
	leader       string
	recent       []string // last full keys under the key, newest last
}

// ---------- usage, backtest, readiness, diff ----------

type AccountUsage struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Count    int       `json:"count"`
	LastUsed time.Time `json:"last_used"`
	InChart  bool      `json:"in_chart"`
}

type CountUsage struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Count    int       `json:"count"`
	LastUsed time.Time `json:"last_used"`
}

type TemplateUsage struct {
	DocCode       string  `json:"doccode"`
	Description   string  `json:"description"`
	JournalsUsing int     `json:"journals_using"`
	MatchCount    int     `json:"match_count"` // journals whose accounts are all in the template
	MatchRate     float64 `json:"match_rate"`
	TopActual     string  `json:"top_actual_accounts,omitempty"`
}

type Usage struct {
	Accounts  []AccountUsage  `json:"accounts"`
	Books     []CountUsage    `json:"journalbooks"`
	DocFormat []CountUsage    `json:"docformats"`
	Parties   []CountUsage    `json:"parties"`
	Templates []TemplateUsage `json:"templates"`
}

type HitStat struct {
	Total       int     `json:"total"`
	Predicted   int     `json:"predicted"`
	HitAccounts int     `json:"hit_accounts"`
	HitFull     int     `json:"hit_full"` // accounts + VAT + WHT
	RateAcc     float64 `json:"rate_accounts"`
	RateFull    float64 `json:"rate_full"`
	Coverage    float64 `json:"coverage"`
}

type Backtest struct {
	Overall  HitStat             `json:"overall"`
	ByLevel  map[Level]*HitStat  `json:"by_level"`
	BySource map[Source]*HitStat `json:"by_source"`
	// Skipped counts how often a level had enough support but was passed over
	// because its leading shape was below the consistency bar.
	Skipped              map[Level]int `json:"skipped_low_consistency"`
	MinConsistency       float64       `json:"min_consistency"`
	MinConsistencyFormat float64       `json:"min_consistency_format"`
	// BaselinePartyDocformat mirrors the earlier mongosh simulation (counterparty
	// code + docformat, any prior occurrence, statements and party-less entries
	// skipped) so its numbers can be checked against the measured ones.
	BaselinePartyDocformat HitStat `json:"baseline_party_docformat"`
}

type Readiness struct {
	Journals     int            `json:"journals"`
	BySource     map[Source]int `json:"by_source"`
	ActiveMonths int            `json:"active_months"`
	FirstEntry   time.Time      `json:"first_entry"`
	LastEntry    time.Time      `json:"last_entry"`
	DecodeErrors int            `json:"decode_errors"`
	Insufficient bool           `json:"insufficient"`
	Reasons      []string       `json:"reasons,omitempty"`
}

type Watermark struct {
	MaxUpdatedAt time.Time `json:"max_updatedat"`
	MaxCreatedAt time.Time `json:"max_createdat"`
	Journals     int       `json:"journals"`
}

type Diff struct {
	PrevGeneratedAt time.Time  `json:"prev_generated_at"`
	Totals          DiffCounts `json:"totals"` // exact counts; the id lists below are capped
	Added           []string   `json:"added"`
	Changed         []string   `json:"changed"`
	Removed         []string   `json:"removed"`
	PatternsChanged []string   `json:"patterns_changed"` // pattern IDs whose winning shape changed
	PatternsNew     []string   `json:"patterns_new"`
	PatternsGone    []string   `json:"patterns_gone"`
}

// Brain is the full export for one shop.
type Brain struct {
	Version     int               `json:"version"`
	GeneratedAt time.Time         `json:"generated_at"`
	Options     Options           `json:"options"`
	Master      MasterData        `json:"master"`
	Readiness   Readiness         `json:"readiness"`
	Usage       Usage             `json:"usage"`
	Patterns    []*Pattern        `json:"patterns"`
	Backtest    Backtest          `json:"backtest"`
	Watermark   Watermark         `json:"watermark"`
	Index       map[string]string `json:"journal_index,omitempty"` // guidfixed → content hash, for the next run's diff
	Diff        *Diff             `json:"diff,omitempty"`

	// Kept in memory (not serialized) for the quiz and "ask the brain".
	entries []entry             // oldest first, same order as guesses
	guesses []Guess             // what the ladder guessed for each entry from the entries before it
	shapes  map[string]Shape    // full key → shape
	byKey   map[string]*Pattern // pattern key → pattern
	chart   map[string]Account
}
