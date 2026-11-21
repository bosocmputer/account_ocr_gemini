package shopbrain

import (
	"context"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
)

var journalProj = proj(
	"guidfixed", "docno", "docdate", "documentref", "batchid", "docformat", "bookcode", "branchcode", "accountdescription", "debtaccounttype",
	"creditor.code", "creditor.taxid", "creditor.branchnumber", "creditor.names.code", "creditor.names.name", "creditor.names.isdelete",
	"debtor.code", "debtor.taxid", "debtor.branchnumber", "debtor.names.code", "debtor.names.name", "debtor.names.isdelete",
	"journaldetail.accountcode", "journaldetail.accountname", "journaldetail.debitamount", "journaldetail.creditamount",
	"vats.vatmode", "vats.vattype", "vats.vatrate", "vats.vatbase", "vats.vatamount", "vats.exceptvat", "vats.custtaxid", "vats.custname", "vats.ischecked",
	"taxes.taxtype", "taxes.conditiontaxtype", "taxes.custtype", "taxes.custtaxid", "taxes.taxamount", "taxes.ischecked",
	"taxes.details.taxbase", "taxes.details.taxrate", "taxes.details.taxamount", "taxes.details.description",
	"createdat", "updatedat",
)

var groupProj = proj("guidfixed", "status", "imagereferences.imageuri", "imagereferences.documentimageguid", "imagereferences.name")

// LoadJournals reads the shop's live (not deleted) journals and its image
// groups with two find({shopid}) queries; the join happens in memory.
func LoadJournals(ctx context.Context, shopID string) ([]Journal, map[string]ImageGroup, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var journals []Journal
	errs, err := findAll(ctx, storage.JournalsCollectionName, shopFilter(shopID), journalProj, &journals)
	if err != nil {
		return nil, nil, errs, err
	}
	// configs.LoadConfig() sets this default; the CLI does not call LoadConfig (it would read .env).
	groupColl := configs.DOCUMENT_IMAGE_GROUP_COLLECTION
	if groupColl == "" {
		groupColl = "documentImageGroups"
	}
	var groups []ImageGroup
	n, err := findAll(ctx, groupColl, shopFilter(shopID), groupProj, &groups)
	errs += n
	if err != nil {
		return nil, nil, errs, err
	}
	byGuid := make(map[string]ImageGroup, len(groups))
	for _, g := range groups {
		byGuid[g.GuidFixed] = g
	}
	return journals, byGuid, errs, nil
}
