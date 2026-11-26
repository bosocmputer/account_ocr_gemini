package shopbrain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The storage.Get* helpers return whole documents; creditors/debtors carry
// addresses, phones, e-mail and auth.username/password, and shops carries
// apikey and owner e-mails. Everything here is read through an explicit
// projection so only whitelisted fields ever leave Mongo.

// notDeleted matches rows without a deletedat (missing or null).
var notDeleted = bson.M{"deletedat": nil}

func proj(fields ...string) bson.M {
	p := bson.M{"_id": 0}
	for _, f := range fields {
		p[f] = 1
	}
	return p
}

// findAll decodes each document on its own so one malformed row is counted
// and skipped instead of failing the whole load.
func findAll[T any](ctx context.Context, coll string, filter bson.M, projection bson.M, out *[]T) (decodeErrors int, err error) {
	cur, err := storage.GetMongoDB().Collection(coll).Find(ctx, filter, options.Find().SetProjection(projection))
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", coll, err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var v T
		if err := cur.Decode(&v); err != nil {
			decodeErrors++
			continue
		}
		*out = append(*out, v)
	}
	return decodeErrors, cur.Err()
}

func shopFilter(shopID string) bson.M {
	f := bson.M{"shopid": shopID}
	for k, v := range notDeleted {
		f[k] = v
	}
	return f
}

// pickName returns the Thai name, else the first non-deleted one.
func pickName(names []rawName) string {
	for _, n := range names {
		if n.Code == "th" && !n.IsDelete && n.Name != "" {
			return n.Name
		}
	}
	for _, n := range names {
		if !n.IsDelete && n.Name != "" {
			return n.Name
		}
	}
	return ""
}

func activeNames(names []rawName) []rawName {
	out := []rawName{}
	for _, n := range names {
		if !n.IsDelete && n.Name != "" {
			out = append(out, n)
		}
	}
	return out
}

type namedRow struct {
	Code      string    `bson:"code"`
	Name1     string    `bson:"name1"`
	Names     []rawName `bson:"names"`
	IsDefault bool      `bson:"isdefault"`
}

func (r namedRow) name() string {
	if n := pickName(r.Names); n != "" {
		return n
	}
	return r.Name1
}

type shopRow struct {
	GuidFixed      string    `bson:"guidfixed"`
	Name1          string    `bson:"name1"`
	Names          []rawName `bson:"names"`
	BranchCode     string    `bson:"branchcode"`
	PromptShopInfo string    `bson:"promptshopinfo"`
	Address        []rawName `bson:"address"`
	Telephone      string    `bson:"telephone"`
	Settings       struct {
		TaxID               string `bson:"taxid"`
		VatTypeSale         *int   `bson:"vattypesale"`
		VatTypePurchase     *int   `bson:"vattypepurchase"`
		InquiryTypeSale     *int   `bson:"inquirytypesale"`
		InquiryTypePurchase *int   `bson:"inquirytypepurchase"`
		IsUseBranch         bool   `bson:"isusebranch"`
		IsUseDepartment     bool   `bson:"isusedepartment"`
		UseBuddhistCalendar bool   `bson:"usebuddhistcalendar"`
		BaseCurrency        string `bson:"base_currency"`
	} `bson:"settings"`
}

type templateRow struct {
	DocCode           string `bson:"doccode"`
	DocFormat         string `bson:"docformat"`
	Description       string `bson:"description"`
	Module            string `bson:"module"`
	BookCode          string `bson:"bookcode"`
	AccountGroup      string `bson:"accountgroup"`
	IsAutoFormat      bool   `bson:"isautoformat"`
	PromptDescription string `bson:"promptdescription"`
	Details           []struct {
		AccountCode        string `bson:"accountcode"`
		Detail             string `bson:"detail"`
		Debit              string `bson:"debit"`
		Credit             string `bson:"credit"`
		IsVatLine          bool   `bson:"isvatline"`
		LinkToDebtAccount  bool   `bson:"linktodebtaccount"`
		IsEntrySelfAccount bool   `bson:"isentryselfaccount"`
		AccountDebit       struct {
			AccountCode string `bson:"accountcode"`
		} `bson:"accountdebit"`
		AccountCredit struct {
			AccountCode string `bson:"accountcode"`
		} `bson:"accountcredit"`
	} `bson:"details"`
}

type partyRow struct {
	Code         string    `bson:"code"`
	Names        []rawName `bson:"names"`
	TaxID        string    `bson:"taxid"`
	BranchNumber string    `bson:"branchnumber"`
	PersonalType int       `bson:"personaltype"`
	CustomerType int       `bson:"customertype"`
	AccountCode  string    `bson:"accountcode"`
	AccountName  string    `bson:"accountname"`
	CreditDay    int       `bson:"creditday"`
}

type itemRow struct {
	Code        string    `bson:"code"`
	Names       []rawName `bson:"names"`
	AccountCode string    `bson:"accountcode"`
	AccountName string    `bson:"accountname"`
}

type bookBankRow struct {
	BankCode  string    `bson:"bankcode"`
	Passbook  string    `bson:"passbook"`
	Names     []rawName `bson:"names"`
	BankNames []rawName `bson:"banknames"`
}

var (
	namedProj    = proj("code", "name1", "names.code", "names.name", "names.isdelete", "isdefault")
	partyProj    = proj("code", "names.code", "names.name", "names.isdelete", "taxid", "branchnumber", "personaltype", "customertype", "accountcode", "accountname", "creditday")
	itemProj     = proj("code", "names.code", "names.name", "names.isdelete", "accountcode", "accountname")
	shopProj     = proj("guidfixed", "name1", "names.code", "names.name", "names.isdelete", "branchcode", "promptshopinfo", "address.code", "address.name", "address.isdelete", "telephone", "settings.taxid", "settings.vattypesale", "settings.vattypepurchase", "settings.inquirytypesale", "settings.inquirytypepurchase", "settings.isusebranch", "settings.isusedepartment", "settings.usebuddhistcalendar", "settings.base_currency")
	templateProj = proj("doccode", "docformat", "description", "module", "bookcode", "accountgroup", "isautoformat", "promptdescription",
		"details.accountcode", "details.detail", "details.debit", "details.credit", "details.isvatline", "details.linktodebtaccount", "details.isentryselfaccount",
		"details.accountdebit.accountcode", "details.accountcredit.accountcode")
)

// LoadMaster reads every master-data collection the brain carries for one shop.
func LoadMaster(ctx context.Context, shopID string) (MasterData, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var m MasterData
	errs := 0
	add := func(n int, err error) error { errs += n; return err }

	var shops []shopRow
	if err := add(findAll(ctx, "shops", bson.M{"guidfixed": shopID}, shopProj, &shops)); err != nil {
		return m, errs, err
	}
	if len(shops) == 0 {
		return m, errs, fmt.Errorf("shop %s not found", shopID)
	}
	s := shops[0]
	m.Shop = ShopInfo{
		ShopID: s.GuidFixed, Name: firstNonEmpty(pickName(s.Names), s.Name1), Names: activeNames(s.Names),
		TaxID: s.Settings.TaxID, BranchCode: s.BranchCode, PromptShopInfo: s.PromptShopInfo,
		Address: activeNames(s.Address), Telephone: s.Telephone,
		VatTypeSale: s.Settings.VatTypeSale, VatTypePurchase: s.Settings.VatTypePurchase,
		InquiryTypeSale: s.Settings.InquiryTypeSale, InquiryTypePurchase: s.Settings.InquiryTypePurchase,
		IsUseBranch: s.Settings.IsUseBranch, IsUseDepartment: s.Settings.IsUseDepartment,
		UseBuddhistCalendar: s.Settings.UseBuddhistCalendar, BaseCurrency: s.Settings.BaseCurrency,
	}

	if err := add(findAll(ctx, "chartofaccounts", shopFilter(shopID),
		proj("accountcode", "accountname", "accountcategory", "accountbalancetype", "financialstatements", "accountgroup", "accountlevel", "consolidateaccountcode"), &m.Accounts)); err != nil {
		return m, errs, err
	}
	sort.Slice(m.Accounts, func(i, j int) bool { return m.Accounts[i].Code < m.Accounts[j].Code })

	named := func(coll string) ([]namedRow, error) {
		var rows []namedRow
		err := add(findAll(ctx, coll, shopFilter(shopID), namedProj, &rows))
		sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
		return rows, err
	}
	toCodeNames := func(rows []namedRow) []CodeName {
		out := make([]CodeName, 0, len(rows))
		for _, r := range rows {
			out = append(out, CodeName{Code: r.Code, Name: r.name()})
		}
		return out
	}
	for _, c := range []struct {
		coll string
		dst  *[]CodeName
	}{
		{"journalBooks", &m.JournalBooks}, {"accountGroups", &m.AccountGroups},
		{"organizationBranches", &m.Branches}, {"organizationDepartments", &m.Departments}, {"bankMaster", &m.Banks},
	} {
		rows, err := named(c.coll)
		if err != nil {
			return m, errs, err
		}
		*c.dst = toCodeNames(rows)
	}
	bt, err := named("organizationBusinessTypes")
	if err != nil {
		return m, errs, err
	}
	for _, r := range bt {
		m.BusinessTypes = append(m.BusinessTypes, BusinessType{Code: r.Code, Name: r.name(), IsDefault: r.IsDefault})
	}

	var tpls []templateRow
	if err := add(findAll(ctx, "documentFormate", shopFilter(shopID), templateProj, &tpls)); err != nil {
		return m, errs, err
	}
	for _, t := range tpls {
		tp := Template{DocCode: t.DocCode, DocFormat: t.DocFormat, Description: t.Description, Module: t.Module, BookCode: t.BookCode,
			AccountGroup: t.AccountGroup, IsAutoFormat: t.IsAutoFormat, PromptDescription: t.PromptDescription}
		for _, d := range t.Details {
			tp.Lines = append(tp.Lines, TemplateLine{AccountCode: d.AccountCode, Detail: d.Detail, Debit: d.Debit, Credit: d.Credit,
				DebitAccount: d.AccountDebit.AccountCode, CreditAccount: d.AccountCredit.AccountCode,
				IsVatLine: d.IsVatLine, LinkToDebtAccount: d.LinkToDebtAccount, IsEntrySelfAccount: d.IsEntrySelfAccount})
		}
		m.Templates = append(m.Templates, tp)
	}
	sort.Slice(m.Templates, func(i, j int) bool { return m.Templates[i].DocCode < m.Templates[j].DocCode })

	if err := add(findAll(ctx, "accountPeriodMaster", shopFilter(shopID), proj("period", "startdate", "enddate", "description", "isdisabled"), &m.Periods)); err != nil {
		return m, errs, err
	}
	sort.Slice(m.Periods, func(i, j int) bool { return m.Periods[i].StartDate.Before(m.Periods[j].StartDate) })
	if err := add(findAll(ctx, "accountPeriodClosed", shopFilter(shopID), proj("accountyear", "accountmonth"), &m.ClosedPeriods)); err != nil {
		return m, errs, err
	}
	sort.Slice(m.ClosedPeriods, func(i, j int) bool {
		a, b := m.ClosedPeriods[i], m.ClosedPeriods[j]
		return a.AccountYear*100+a.AccountMonth < b.AccountYear*100+b.AccountMonth
	})

	for _, c := range []struct {
		coll, kind string
		dst        *[]Party
	}{{"creditors", "creditor", &m.Creditors}, {"debtors", "debtor", &m.Debtors}} {
		var rows []partyRow
		if err := add(findAll(ctx, c.coll, shopFilter(shopID), partyProj, &rows)); err != nil {
			return m, errs, err
		}
		for _, r := range rows {
			*c.dst = append(*c.dst, Party{Kind: c.kind, Code: r.Code, Name: pickName(r.Names), TaxID: r.TaxID, BranchNumber: r.BranchNumber,
				PersonalType: r.PersonalType, CustomerType: r.CustomerType, AccountCode: r.AccountCode, AccountName: r.AccountName, CreditDay: r.CreditDay})
		}
		sort.Slice(*c.dst, func(i, j int) bool { return (*c.dst)[i].Code < (*c.dst)[j].Code })
	}

	for _, c := range []struct {
		coll string
		dst  *[]MasterItem
	}{{"masterIncomes", &m.MasterIncomes}, {"masterExpenses", &m.MasterExpenses}} {
		var rows []itemRow
		if err := add(findAll(ctx, c.coll, shopFilter(shopID), itemProj, &rows)); err != nil {
			return m, errs, err
		}
		for _, r := range rows {
			*c.dst = append(*c.dst, MasterItem{Code: r.Code, Name: pickName(r.Names), AccountCode: r.AccountCode, AccountName: r.AccountName})
		}
	}

	var banks []bookBankRow
	if err := add(findAll(ctx, "bookBank", shopFilter(shopID), proj("bankcode", "passbook", "names.code", "names.name", "names.isdelete", "banknames.code", "banknames.name", "banknames.isdelete"), &banks)); err != nil {
		return m, errs, err
	}
	for _, b := range banks {
		m.BookBanks = append(m.BookBanks, BookBank{BankCode: b.BankCode, Name: pickName(b.Names), BankName: pickName(b.BankNames), PassbookLast4: last4(b.Passbook)})
	}
	return m, errs, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func last4(s string) string {
	digits := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) <= 4 {
		return string(digits)
	}
	return string(digits[len(digits)-4:])
}
