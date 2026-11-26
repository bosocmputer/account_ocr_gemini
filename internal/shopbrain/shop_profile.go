package shopbrain

import "strings"

type ShopProfile struct {
	Names     []string  `json:"names"`
	TaxID     string    `json:"taxid"`
	Branch    string    `json:"branch"`
	Address   []rawName `json:"address"`
	Telephone string    `json:"telephone"`
}

func profileOf(shop ShopInfo, aliases []string) ShopProfile {
	names := make([]string, 0, len(shop.Names)+len(aliases)+1)
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		key := normalizeName(name)
		if key != "" && !seen[key] {
			names = append(names, name)
			seen[key] = true
		}
	}
	add(shop.Name)
	for _, name := range shop.Names {
		if !name.IsDelete {
			add(name.Name)
		}
	}
	for _, name := range aliases {
		add(name)
	}
	return ShopProfile{Names: names, TaxID: shop.TaxID, Branch: shop.BranchCode, Address: shop.Address, Telephone: shop.Telephone}
}

type billParty struct {
	Name  string
	TaxID string
}

type billClassification struct {
	Direction    string
	Counterparty billParty
	MatchedBy    string
	Reason       string
}

func classifyBill(profile ShopProfile, seller, buyer billParty) billClassification {
	shopTax := validTaxID(profile.TaxID)
	match := func(party billParty) string {
		if tax := validTaxID(party.TaxID); tax != "" && shopTax != "" {
			if tax == shopTax {
				return "taxid"
			}
			return ""
		}
		if name := normalizeName(party.Name); name != "" {
			for _, own := range profile.Names {
				if name == normalizeName(own) {
					return "name"
				}
			}
		}
		return ""
	}
	s, b := match(seller), match(buyer)
	switch {
	case s != "" && b != "":
		return billClassification{Direction: "unknown", Reason: "ข้อมูลร้านตรงทั้งผู้ขายและผู้ซื้อ กรุณาตรวจหัวบิล"}
	case s != "":
		return billClassification{Direction: "sale", MatchedBy: s, Counterparty: buyer, Reason: "ร้านตรงกับผู้ขายบนบิล"}
	case b != "":
		return billClassification{Direction: "purchase", MatchedBy: b, Counterparty: seller, Reason: "ร้านตรงกับผู้ซื้อบนบิล"}
	default:
		return billClassification{Direction: "unknown", Reason: "ไม่พบชื่อหรือเลขผู้เสียภาษีของร้านในหัวบิลทั้งสองฝั่ง"}
	}
}
