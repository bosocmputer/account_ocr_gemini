package shopbrain

import "testing"

func TestClassifyBill(t *testing.T) {
	p := ShopProfile{Names: []string{"บริษัท สมองร้าน จำกัด", "ชื่อบนบิล"}, TaxID: "0105500000001"}
	shop := billParty{TaxID: p.TaxID}
	other := billParty{Name: "คู่ค้า", TaxID: "0105500000002"}
	alias := billParty{Name: "ชื่อบนบิล"}
	for _, tc := range []struct {
		name               string
		seller, buyer      billParty
		direction, matched string
	}{
		{"purchase", other, shop, "purchase", "taxid"},
		{"sale", shop, other, "sale", "taxid"},
		{"alias", alias, other, "sale", "name"},
		{"both", alias, shop, "unknown", ""},
		{"neither", other, billParty{}, "unknown", ""},
		{"different tax ID", billParty{Name: alias.Name, TaxID: other.TaxID}, billParty{}, "unknown", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := classifyBill(p, tc.seller, tc.buyer)
			if r.Direction != tc.direction || r.MatchedBy != tc.matched || r.Reason == "" {
				t.Fatalf("got %+v", r)
			}
		})
	}
}
