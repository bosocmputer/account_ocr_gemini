package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
)

// Fixture task used throughout the batch-OCR plan's verification steps —
// its eligible/skipped counts were confirmed by hand against Mongo Compass
// before this feature was built (see plan-clever-lemon.md TODO-0):
// status:1 total 41, of which 12 have neither ocranalyzeai nor references
// (eligible), and 29 have one or both already (skipped).
const (
	testShopID   = "36xq3C3RKkSrkcCJNj6lnjfBl6Z"
	testTaskGuid = "3BLFbYgKMUkMexKrZSynNBkwP6x"
)

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupLiveMongoTest(t *testing.T) {
	t.Helper()
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")

	// Take the collection name from config rather than hardcoding it here,
	// so a wrong default (or a bad DOCUMENT_IMAGE_GROUP_COLLECTION override)
	// fails these tests instead of being masked by the test's own literal.
	configs.DOCUMENT_IMAGE_GROUP_COLLECTION = getEnvOrDefault("DOCUMENT_IMAGE_GROUP_COLLECTION", "documentImageGroups")
	if configs.DOCUMENT_IMAGE_GROUP_COLLECTION != "documentImageGroups" {
		t.Logf("note: DOCUMENT_IMAGE_GROUP_COLLECTION overridden to %q", configs.DOCUMENT_IMAGE_GROUP_COLLECTION)
	}

	if err := InitMongoDB(); err != nil {
		t.Fatalf("mongo connect failed: %v", err)
	}
	t.Cleanup(CloseMongoDB)
}

func TestListEligibleGroupsForTask_MatchesKnownFixtureCount(t *testing.T) {
	setupLiveMongoTest(t)

	groups, err := ListEligibleGroupsForTask(testShopID, testTaskGuid)
	if err != nil {
		t.Fatalf("ListEligibleGroupsForTask failed: %v", err)
	}

	if len(groups) != 41 {
		t.Fatalf("expected 41 status:1 groups for fixture task, got %d", len(groups))
	}

	eligible, skipped := 0, 0
	for _, g := range groups {
		if g.HasOcrResult() || g.IsAlreadyRecorded() {
			skipped++
		} else {
			eligible++
		}
	}

	if eligible != 12 {
		t.Errorf("expected 12 eligible groups, got %d", eligible)
	}
	if skipped != 29 {
		t.Errorf("expected 29 skipped groups, got %d", skipped)
	}
}

func TestHasOcrResult_TreatsMissingFieldAsNotAnalyzed(t *testing.T) {
	withResult := DocumentImageGroupRef{OcrAnalyzeAI: `{"accounting_entry":{}}`}
	if !withResult.HasOcrResult() {
		t.Error("expected HasOcrResult true when ocranalyzeai is a non-empty JSON string")
	}

	blank := DocumentImageGroupRef{OcrAnalyzeAI: ""}
	if blank.HasOcrResult() {
		t.Error("expected HasOcrResult false when ocranalyzeai is empty string")
	}

	// Zero value covers the "field absent from document entirely" case —
	// this is the case that a naive `ocranalyzeai: ""` filter would miss.
	var missing DocumentImageGroupRef
	if missing.HasOcrResult() {
		t.Error("expected HasOcrResult false when ocranalyzeai field is absent (zero value)")
	}
}

func TestIsAlreadyRecorded(t *testing.T) {
	none := DocumentImageGroupRef{}
	if none.IsAlreadyRecorded() {
		t.Error("expected IsAlreadyRecorded false with no references")
	}

	var withRef DocumentImageGroupRef
	withRef.References = append(withRef.References, struct {
		GuidFixed string `bson:"guidfixed"`
		Module    string `bson:"module"`
		DocNo     string `bson:"docno"`
	}{GuidFixed: "x", Module: "GL", DocNo: "JV-1"})
	if !withRef.IsAlreadyRecorded() {
		t.Error("expected IsAlreadyRecorded true when references is non-empty")
	}
}

func TestRefreshGroupStates_EmptyGuidsReturnsEmptyMapWithoutQuerying(t *testing.T) {
	// Does not call setupLiveMongoTest — this must short-circuit before
	// touching Mongo at all, so it should pass even without a live DB.
	result, err := RefreshGroupStates(testShopID, testTaskGuid, nil)
	if err != nil {
		t.Fatalf("expected no error for empty guids, got %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty map for empty guids, got %d entries", len(result))
	}
}

func TestRefreshGroupStates_MatchesListEligibleGroupsForTask(t *testing.T) {
	setupLiveMongoTest(t)

	groups, err := ListEligibleGroupsForTask(testShopID, testTaskGuid)
	if err != nil {
		t.Fatalf("ListEligibleGroupsForTask failed: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("fixture task returned no groups — cannot verify RefreshGroupStates")
	}

	guids := make([]string, len(groups))
	for i, g := range groups {
		guids[i] = g.GuidFixed
	}

	states, err := RefreshGroupStates(testShopID, testTaskGuid, guids)
	if err != nil {
		t.Fatalf("RefreshGroupStates failed: %v", err)
	}

	if len(states) != len(groups) {
		t.Fatalf("expected RefreshGroupStates to return %d groups, got %d", len(groups), len(states))
	}

	for _, g := range groups {
		refreshed, ok := states[g.GuidFixed]
		if !ok {
			t.Errorf("guidfixed %s missing from RefreshGroupStates result", g.GuidFixed)
			continue
		}
		if refreshed.HasOcrResult() != g.HasOcrResult() {
			t.Errorf("guidfixed %s: HasOcrResult mismatch between ListEligibleGroupsForTask (%v) and RefreshGroupStates (%v)",
				g.GuidFixed, g.HasOcrResult(), refreshed.HasOcrResult())
		}
		if refreshed.IsAlreadyRecorded() != g.IsAlreadyRecorded() {
			t.Errorf("guidfixed %s: IsAlreadyRecorded mismatch between ListEligibleGroupsForTask (%v) and RefreshGroupStates (%v)",
				g.GuidFixed, g.IsAlreadyRecorded(), refreshed.IsAlreadyRecorded())
		}
	}
}

// ocranalyzeaiState describes how a document currently represents "not yet
// analyzed", which this collection does in three different ways.
type ocranalyzeaiState int

const (
	stateMissing ocranalyzeaiState = iota // field absent from the document entirely
	stateEmpty                            // field present, set to ""
	stateNull                             // field present, set to null
)

func (s ocranalyzeaiState) String() string {
	switch s {
	case stateMissing:
		return "missing"
	case stateEmpty:
		return `empty-string`
	default:
		return "null"
	}
}

// findEligibleGroupInState locates a group in the fixture task that is
// unanalyzed and unrecorded AND whose ocranalyzeai is in the requested
// state, so a test can target one specific representation rather than
// whichever document happens to sort first.
func findEligibleGroupInState(t *testing.T, want ocranalyzeaiState) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var stateFilter bson.M
	switch want {
	case stateMissing:
		stateFilter = bson.M{"ocranalyzeai": bson.M{"$exists": false}}
	case stateEmpty:
		stateFilter = bson.M{"ocranalyzeai": ""}
	default:
		stateFilter = bson.M{"ocranalyzeai": nil, "$and": []bson.M{{"ocranalyzeai": bson.M{"$exists": true}}}}
	}

	filter := bson.M{
		"shopid":   testShopID,
		"taskguid": testTaskGuid,
		"status":   1,
		"$or": []bson.M{
			{"references": bson.M{"$exists": false}},
			{"references": bson.M{"$size": 0}},
		},
	}
	for k, v := range stateFilter {
		filter[k] = v
	}

	var doc struct {
		GuidFixed string `bson:"guidfixed"`
	}
	err := GetMongoDB().Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION).
		FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		t.Skipf("no eligible group with ocranalyzeai %s in fixture task: %v", want, err)
	}
	return doc.GuidFixed
}

// restoreOcrAnalyzeAI puts a document back into the exact state it was in
// before a test wrote to it. Blindly $unset-ing would be wrong: a document
// that started as `ocranalyzeai: ""` would come back as a document with no
// such field, which is a different state — and this is another team's
// collection, so tests must not quietly reshape its documents.
func restoreOcrAnalyzeAI(t *testing.T, guidfixed string, original ocranalyzeaiState) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var update bson.M
		switch original {
		case stateMissing:
			update = bson.M{"$unset": bson.M{"ocranalyzeai": ""}}
		case stateEmpty:
			update = bson.M{"$set": bson.M{"ocranalyzeai": ""}}
		default:
			update = bson.M{"$set": bson.M{"ocranalyzeai": nil}}
		}

		_, err := GetMongoDB().Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION).UpdateOne(
			ctx,
			bson.M{"shopid": testShopID, "guidfixed": guidfixed},
			update,
		)
		if err != nil {
			t.Errorf("cleanup: failed to restore ocranalyzeai=%s on %s: %v — dev DB may be left dirty", original, guidfixed, err)
		}
	})
}

// TestSetGroupOcrAnalyzeAI_RoundTrip runs the write path against each of the
// three ways this collection represents "not yet analyzed", rather than
// against whichever eligible document happens to sort first.
//
// That distinction matters: of the 41 status:1 documents in the fixture
// task, only ONE has ocranalyzeai missing entirely while 27 have it set to
// "". A test that just takes the first eligible document is therefore one
// _id-ordering change (or one edited document) away from silently no longer
// covering the missing-field case — which is the exact case the $or filter
// exists for and the exact case whose failure mode is "pay for AI, discard
// the result, report success".
//
// This mutates real documents in the shared dev database, but only ones
// already eligible (unanalyzed, unrecorded), only the ocranalyzeai field,
// and each is restored to its original representation afterward.
func TestSetGroupOcrAnalyzeAI_RoundTrip(t *testing.T) {
	setupLiveMongoTest(t)

	for _, original := range []ocranalyzeaiState{stateMissing, stateEmpty, stateNull} {
		t.Run(original.String(), func(t *testing.T) {
			guidfixed := findEligibleGroupInState(t, original)
			restoreOcrAnalyzeAI(t, guidfixed, original)

			testPayload := `{"_test":"TestSetGroupOcrAnalyzeAI_RoundTrip/` + original.String() + `"}`

			written, err := SetGroupOcrAnalyzeAI(testShopID, guidfixed, testPayload)
			if err != nil {
				t.Fatalf("SetGroupOcrAnalyzeAI failed: %v", err)
			}
			if !written {
				t.Fatalf("expected written=true for eligible group %s with ocranalyzeai %s — "+
					"this is the case the $or filter exists for; a false here means the batch worker "+
					"would pay for AI and silently discard the result", guidfixed, original)
			}

			// A second write must report written=false, not an error: the
			// field is now populated, which is how the worker detects
			// "someone else got here first" and marks the item skipped.
			writtenAgain, err := SetGroupOcrAnalyzeAI(testShopID, guidfixed, testPayload)
			if err != nil {
				t.Fatalf("second SetGroupOcrAnalyzeAI call failed: %v", err)
			}
			if writtenAgain {
				t.Errorf("expected written=false on second write to %s (ocranalyzeai is populated now)", guidfixed)
			}

			refreshed, err := RefreshGroupStates(testShopID, testTaskGuid, []string{guidfixed})
			if err != nil {
				t.Fatalf("RefreshGroupStates failed: %v", err)
			}
			g, ok := refreshed[guidfixed]
			if !ok {
				t.Fatalf("guidfixed %s missing from RefreshGroupStates after write", guidfixed)
			}
			if g.OcrAnalyzeAI != testPayload {
				t.Errorf("expected ocranalyzeai %q after write, got %q", testPayload, g.OcrAnalyzeAI)
			}
			if !g.HasOcrResult() {
				t.Errorf("expected HasOcrResult() true after a successful write to %s", guidfixed)
			}
		})
	}
}

// TestSetGroupOcrAnalyzeAI_DeletedDocumentReturnsError distinguishes the two
// reasons a write can fail to match: an already-populated document (skip,
// not an error) versus a document that no longer exists (a real error the
// worker must not silently swallow as "skipped").
func TestSetGroupOcrAnalyzeAI_DeletedDocumentReturnsError(t *testing.T) {
	setupLiveMongoTest(t)

	written, err := SetGroupOcrAnalyzeAI(testShopID, "guid-that-does-not-exist-batchocr-test", `{"x":1}`)
	if written {
		t.Error("expected written=false for a nonexistent document")
	}
	if err == nil {
		t.Error("expected an error for a nonexistent document — the worker relies on this to avoid marking a vanished document as merely skipped")
	}
}
