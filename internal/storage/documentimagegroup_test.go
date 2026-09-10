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

func setupLiveMongoTest(t *testing.T) {
	t.Helper()
	godotenv.Load("../../.env")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI not set — skipping live-DB integration test")
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	configs.DOCUMENT_IMAGE_GROUP_COLLECTION = "documentImageGroups"
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

// TestSetGroupOcrAnalyzeAI_RoundTrip picks one confirmed-eligible group from
// the fixture task (no ocranalyzeai, no references), writes a harmless
// placeholder result to it, and verifies the write matches. This directly
// exercises the $or filter's most important case: writing to a document
// whose ocranalyzeai field may not exist at all (see SetGroupOcrAnalyzeAI's
// doc comment on why `ocranalyzeai: ""` alone would silently no-op here).
//
// This test mutates real data in the shared dev database — it only touches
// a group that is already eligible (unanalyzed, unrecorded) in the known
// fixture task, and only sets ocranalyzeai, which is exactly what the batch
// worker itself would do to that same document.
func TestSetGroupOcrAnalyzeAI_RoundTrip(t *testing.T) {
	setupLiveMongoTest(t)

	groups, err := ListEligibleGroupsForTask(testShopID, testTaskGuid)
	if err != nil {
		t.Fatalf("ListEligibleGroupsForTask failed: %v", err)
	}

	var target *DocumentImageGroupRef
	for i := range groups {
		if !groups[i].HasOcrResult() && !groups[i].IsAlreadyRecorded() {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		t.Skip("no eligible (unanalyzed, unrecorded) group found in fixture task — cannot test write path")
	}

	// This mutates a real shared document, so restore it to its original
	// (unset) state afterward regardless of test outcome — this is someone
	// else's collection, not a scratch one we own.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := GetMongoDB().Collection(configs.DOCUMENT_IMAGE_GROUP_COLLECTION).UpdateOne(
			ctx,
			bson.M{"shopid": testShopID, "guidfixed": target.GuidFixed},
			bson.M{"$unset": bson.M{"ocranalyzeai": ""}},
		)
		if err != nil {
			t.Logf("cleanup: failed to unset ocranalyzeai on %s: %v", target.GuidFixed, err)
		}
	})

	const testPayload = `{"_test":"documentimagegroup_test.go TestSetGroupOcrAnalyzeAI_RoundTrip"}`

	written, err := SetGroupOcrAnalyzeAI(testShopID, target.GuidFixed, testPayload)
	if err != nil {
		t.Fatalf("SetGroupOcrAnalyzeAI failed: %v", err)
	}
	if !written {
		t.Fatalf("expected written=true for a confirmed-eligible group %s (this is exactly the missing-field case the $or filter exists for)", target.GuidFixed)
	}

	// Immediately trying again must report written=false — the field is no
	// longer empty/absent, so this exercises the "someone already wrote a
	// result" branch (which the batch worker treats as skip-not-error).
	writtenAgain, err := SetGroupOcrAnalyzeAI(testShopID, target.GuidFixed, testPayload)
	if err != nil {
		t.Fatalf("second SetGroupOcrAnalyzeAI call failed: %v", err)
	}
	if writtenAgain {
		t.Errorf("expected written=false on second write to the same group (ocranalyzeai is no longer empty/absent)")
	}

	refreshed, err := RefreshGroupStates(testShopID, testTaskGuid, []string{target.GuidFixed})
	if err != nil {
		t.Fatalf("RefreshGroupStates failed: %v", err)
	}
	if g, ok := refreshed[target.GuidFixed]; !ok || g.OcrAnalyzeAI != testPayload {
		t.Errorf("expected ocranalyzeai to be set to test payload after write, got %+v (found=%v)", g, ok)
	}
}
