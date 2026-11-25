package shopbrain

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The owner's verdicts are kept in a separate MongoDB (BRAIN_MONGO_URI): the
// main database is read-only for billscan. No token, e-mail or user name is stored.

var ErrStoreUnavailable = errors.New("feedback store unavailable")

const (
	VerdictCorrect      = "brain_correct"
	VerdictWrong        = "brain_wrong"
	VerdictJournalWrong = "journal_wrong"
	VerdictUnknownOK    = "unknown_ok"
	VerdictShouldKnow   = "unknown_should_know"
	VerdictBothOK       = "both_ok" // brain and saved journal differ, but both are acceptable
)

var validVerdicts = map[string]bool{VerdictCorrect: true, VerdictWrong: true, VerdictJournalWrong: true, VerdictUnknownOK: true, VerdictShouldKnow: true, VerdictBothOK: true}

type PredictionRecord struct {
	Known       bool    `bson:"known" json:"known"`
	Level       Level   `bson:"level" json:"level"`
	PatternID   string  `bson:"pattern_id,omitempty" json:"pattern_id,omitempty"`
	Accounts    string  `bson:"accounts,omitempty" json:"accounts,omitempty"`
	Full        string  `bson:"full,omitempty" json:"full,omitempty"`
	Support     int     `bson:"support,omitempty" json:"support,omitempty"`
	Consistency float64 `bson:"consistency,omitempty" json:"consistency,omitempty"`
	// for "unknown": what the first skipped level would have said
	WouldBe      string  `bson:"would_be,omitempty" json:"would_be,omitempty"`
	WouldBeLevel Level   `bson:"would_be_level,omitempty" json:"would_be_level,omitempty"`
	WouldBeCons  float64 `bson:"would_be_consistency,omitempty" json:"would_be_consistency,omitempty"`
}

type Feedback struct {
	ShopID               string           `bson:"shopid" json:"-"`
	Mode                 string           `bson:"mode" json:"mode"` // quiz | ask
	JournalGUID          string           `bson:"journal_guid,omitempty" json:"journal_guid,omitempty"`
	DocNo                string           `bson:"docno,omitempty" json:"docno,omitempty"`
	Source               Source           `bson:"source,omitempty" json:"source,omitempty"`
	Input                *AskInput        `bson:"input,omitempty" json:"input,omitempty"`
	Prediction           PredictionRecord `bson:"prediction" json:"prediction"`
	Actual               string           `bson:"actual,omitempty" json:"actual,omitempty"` // quiz: full key of the saved journal
	AutoMatchAccounts    bool             `bson:"auto_match_accounts" json:"auto_match_accounts"`
	Verdict              string           `bson:"verdict" json:"verdict"`
	CorrectAccounts      string           `bson:"correct_accounts,omitempty" json:"correct_accounts,omitempty"`
	Note                 string           `bson:"note,omitempty" json:"note,omitempty"`
	BrainVersion         int              `bson:"brain_version" json:"brain_version"`
	MinConsistency       float64          `bson:"min_consistency" json:"min_consistency"`
	MinConsistencyFormat float64          `bson:"min_consistency_format" json:"min_consistency_format"`
	CreatedAt            time.Time        `bson:"created_at" json:"created_at"`
}

type FeedbackStore interface {
	Save(ctx context.Context, f Feedback) error
	List(ctx context.Context, shopID string) ([]Feedback, error)
}

// ---------- summary ----------

type VerdictStat struct {
	Judged     int     `json:"judged"`  // brain_correct + brain_wrong
	Correct    int     `json:"correct"` // brain_correct
	Rate       float64 `json:"rate"`
	UnknownOK  int     `json:"unknown_ok"`
	ShouldKnow int     `json:"unknown_should_know"`
	JournalBad int     `json:"journal_wrong"`
	BothOK     int     `json:"both_ok"`
	Total      int     `json:"total"`
}

type FeedbackSummary struct {
	Total        int                     `json:"total"`
	Overall      VerdictStat             `json:"overall"`
	ByLevel      map[Level]*VerdictStat  `json:"by_level"`
	ByMode       map[string]*VerdictStat `json:"by_mode"`
	JournalWrong []Feedback              `json:"journal_wrong"`
	Recent       []Feedback              `json:"recent"`
}

func (s *VerdictStat) add(v string) {
	s.Total++
	switch v {
	case VerdictCorrect:
		s.Judged++
		s.Correct++
	case VerdictBothOK:
		s.Judged++
		s.Correct++
		s.BothOK++
	case VerdictWrong:
		s.Judged++
	case VerdictUnknownOK:
		s.UnknownOK++
	case VerdictShouldKnow:
		s.ShouldKnow++
	case VerdictJournalWrong:
		s.JournalBad++
	}
	if s.Judged > 0 {
		s.Rate = round4(float64(s.Correct) / float64(s.Judged))
	}
}

// Summarize counts verdicts; "journal_wrong" is not counted against the brain.
func Summarize(list []Feedback) FeedbackSummary {
	sort.SliceStable(list, func(a, b int) bool { return list[a].CreatedAt.After(list[b].CreatedAt) })
	s := FeedbackSummary{Total: len(list), ByLevel: map[Level]*VerdictStat{}, ByMode: map[string]*VerdictStat{}}
	for _, f := range list {
		s.Overall.add(f.Verdict)
		if s.ByLevel[f.Prediction.Level] == nil {
			s.ByLevel[f.Prediction.Level] = &VerdictStat{}
		}
		s.ByLevel[f.Prediction.Level].add(f.Verdict)
		if s.ByMode[f.Mode] == nil {
			s.ByMode[f.Mode] = &VerdictStat{}
		}
		s.ByMode[f.Mode].add(f.Verdict)
		if f.Verdict == VerdictJournalWrong && len(s.JournalWrong) < 50 {
			s.JournalWrong = append(s.JournalWrong, f)
		}
	}
	if len(list) > 20 {
		s.Recent = list[:20]
	} else {
		s.Recent = list
	}
	return s
}

// ---------- Mongo store ----------

// MongoFeedback connects lazily so billscan starts even when the store is
// down; a failed connect is retried at most every 30 seconds.
type MongoFeedback struct {
	URI, DB string

	mu       sync.Mutex
	db       *mongo.Database
	coll     *mongo.Collection
	lastFail time.Time
}

func NewMongoFeedback(uri, db string) *MongoFeedback {
	if db == "" {
		db = "shopbrain"
	}
	return &MongoFeedback{URI: uri, DB: db}
}

func (m *MongoFeedback) collection(ctx context.Context) (*mongo.Collection, error) {
	if m == nil || m.URI == "" {
		return nil, ErrStoreUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.coll != nil {
		return m.coll, nil
	}
	if time.Since(m.lastFail) < 30*time.Second {
		return nil, ErrStoreUnavailable
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := mongo.Connect(cctx, options.Client().ApplyURI(m.URI).SetServerSelectionTimeout(5*time.Second))
	if err == nil {
		err = client.Ping(cctx, nil)
	}
	if err != nil {
		m.lastFail = time.Now()
		if client != nil {
			_ = client.Disconnect(context.Background())
		}
		return nil, errors.Join(ErrStoreUnavailable, err)
	}
	db := client.Database(m.DB)
	coll := db.Collection("brain_feedback")
	_, _ = coll.Indexes().CreateMany(cctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "shopid", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "shopid", Value: 1}, {Key: "journal_guid", Value: 1}}},
	})
	_, _ = db.Collection(mcpTokensColl).Indexes().CreateMany(cctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "hash", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "shopid", Value: 1}, {Key: "created_at", Value: -1}}},
	})
	_, _ = db.Collection(mcpCallsColl).Indexes().CreateMany(cctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "token_id", Value: 1}, {Key: "at", Value: -1}}},
	})
	m.db, m.coll = db, coll
	return coll, nil
}

// database returns the brain's own database (same lazy connection as feedback).
func (m *MongoFeedback) database(ctx context.Context) (*mongo.Database, error) {
	if _, err := m.collection(ctx); err != nil {
		return nil, err
	}
	return m.db, nil
}

func (m *MongoFeedback) Save(ctx context.Context, f Feedback) error {
	coll, err := m.collection(ctx)
	if err != nil {
		return err
	}
	_, err = coll.InsertOne(ctx, f)
	return err
}

func (m *MongoFeedback) List(ctx context.Context, shopID string) ([]Feedback, error) {
	coll, err := m.collection(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := coll.Find(ctx, bson.M{"shopid": shopID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(5000))
	if err != nil {
		return nil, err
	}
	var out []Feedback
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
