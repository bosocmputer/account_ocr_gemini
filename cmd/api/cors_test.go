package main

import "testing"

func TestKnowledgeCorsMethods(t *testing.T) {
	if got := corsMethods("/billscan/api/v1/shop-brain/knowledge/some-id"); got != "POST, GET, PUT, DELETE, OPTIONS" {
		t.Fatalf("knowledge methods = %q", got)
	}
	if got := corsMethods("/billscan/api/v1/shop-brain"); got != "POST, GET, OPTIONS" {
		t.Fatalf("existing methods = %q", got)
	}
}
