package processor

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestExtractVendorNameFromOCR(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"mistral image placeholder first (production log)", "![img-0.jpeg](img-0.jpeg)\n\nบริษัท ภูพิงค์แอร์เอ็นจิเนียริ่ง จำกัด\n1/14 หมู่ 2", "บริษัท ภูพิงค์แอร์เอ็นจิเนียริ่ง จำกัด"},
		{"plain text, no placeholder (old behaviour)", "บริษัท เอบีซี จำกัด\nที่อยู่", "บริษัท เอบีซี จำกัด"},
		{"several images before the name", "![img-0.jpeg](img-0.jpeg)\n![img-1.jpeg](img-1.jpeg)\n\nห้างหุ้นส่วนจำกัด สยามการค้า", "ห้างหุ้นส่วนจำกัด สยามการค้า"},
		{"heading and bold markers removed", "## **บริษัท เอบีซี จำกัด**", "บริษัท เอบีซี จำกัด"},
		{"short lines such as page numbers are skipped", "1\n2/3\nบริษัท ตัวอย่าง จำกัด", "บริษัท ตัวอย่าง จำกัด"},
		{"windows line endings", "![img-0.jpeg](img-0.jpeg)\r\nบริษัท เอบีซี จำกัด\r\n", "บริษัท เอบีซี จำกัด"},
		{"only an image", "![img-0.jpeg](img-0.jpeg)", ""},
		{"empty", "", ""},
		{"text that merely contains an image tag is kept", "ใบเสร็จรับเงิน ![x](y)", "ใบเสร็จรับเงิน ![x](y)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractVendorNameFromOCR(c.text); got != c.want {
				t.Fatalf("want %q, got %q", c.want, got)
			}
		})
	}
}

// The reason it matters: with the placeholder as the "name", the matcher can never find the vendor.
func TestPlaceholderNeverMatchedButRealNameDoes(t *testing.T) {
	creditors := []bson.M{{
		"code":  "AP0001",
		"names": bson.A{bson.M{"code": "th", "name": "บริษัท ภูพิงค์แอร์เอ็นจิเนียริ่ง จำกัด"}},
	}}
	if r := MatchVendor("![img-0.jpeg](img-0.jpeg)", creditors, ""); r.Found {
		t.Fatalf("placeholder must not match, got %+v", r)
	}
	name := ExtractVendorNameFromOCR("![img-0.jpeg](img-0.jpeg)\n\nบริษัท ภูพิงค์แอร์เอ็นจิเนียริ่ง จำกัด\n")
	if r := MatchVendor(name, creditors, ""); !r.Found || r.Code != "AP0001" {
		t.Fatalf("expected AP0001, got %+v", r)
	}
}
