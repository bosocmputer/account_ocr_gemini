package shopbrain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MCP connection tokens: one shop each, read-only, revocable, 90 days.
// Only a SHA-256 hash is stored, so a token is shown to the owner once.

const (
	mcpTokensColl  = "mcp_tokens"
	mcpCallsColl   = "mcp_calls"
	MCPTokenPrefix = "sbk_"
	MCPTokenTTL    = 90 * 24 * time.Hour
	mcpMaxTokens   = 20 // active tokens per shop
)

var (
	ErrTokenInvalid = errors.New("mcp token invalid")
	ErrTokenExpired = errors.New("mcp token expired")
	ErrTokenRevoked = errors.New("mcp token revoked")
	ErrTokenLimit   = errors.New("too many active mcp tokens")
)

// MCPToken is what the owner sees in the token list (never the token itself).
type MCPToken struct {
	ID         string     `bson:"_id" json:"id"`
	ShopID     string     `bson:"shopid" json:"shopid"`
	Hash       string     `bson:"hash" json:"-"`
	Hint       string     `bson:"hint" json:"hint"` // "sbk_ab12…9f3c" for recognising it
	Name       string     `bson:"name" json:"name"`
	CreatedAt  time.Time  `bson:"created_at" json:"created_at"`
	ExpiresAt  time.Time  `bson:"expires_at" json:"expires_at"`
	LastUsedAt *time.Time `bson:"last_used_at,omitempty" json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	Calls      int        `bson:"calls" json:"calls"`
}

// Status: active | expired | revoked.
func (t MCPToken) Status(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case !now.Before(t.ExpiresAt):
		return "expired"
	}
	return "active"
}

// MCPCall is the usage log: which token called which tool, when. The
// arguments and answers are not stored.
type MCPCall struct {
	TokenID string    `bson:"token_id"`
	ShopID  string    `bson:"shopid"`
	Tool    string    `bson:"tool"`
	OK      bool      `bson:"ok"`
	At      time.Time `bson:"at"`
}

type MCPTokenStore interface {
	CreateToken(ctx context.Context, t MCPToken) error
	ListTokens(ctx context.Context, shopID string) ([]MCPToken, error)
	RevokeToken(ctx context.Context, shopID, id string, at time.Time) error
	FindToken(ctx context.Context, hash string) (*MCPToken, error)
	TouchToken(ctx context.Context, id string, at time.Time) error
	LogCall(ctx context.Context, c MCPCall) error
}

// NewMCPToken makes a fresh token and its stored record.
func NewMCPToken(shopID, name string, now time.Time) (plain string, rec MCPToken, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", MCPToken{}, err
	}
	id := make([]byte, 6)
	if _, err = rand.Read(id); err != nil {
		return "", MCPToken{}, err
	}
	plain = MCPTokenPrefix + hex.EncodeToString(raw)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	rec = MCPToken{
		ID: hex.EncodeToString(id), ShopID: shopID, Hash: HashMCPToken(plain), Name: name,
		Hint:      plain[:8] + "…" + plain[len(plain)-4:],
		CreatedAt: now, ExpiresAt: now.Add(MCPTokenTTL),
	}
	return plain, rec, nil
}

func HashMCPToken(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// ---------- Mongo (same database as the feedback) ----------

func (m *MongoFeedback) tokens(ctx context.Context) (*mongo.Collection, error) {
	db, err := m.database(ctx)
	if err != nil {
		return nil, err
	}
	return db.Collection(mcpTokensColl), nil
}

func (m *MongoFeedback) CreateToken(ctx context.Context, t MCPToken) error {
	c, err := m.tokens(ctx)
	if err != nil {
		return err
	}
	active, err := c.CountDocuments(ctx, bson.M{"shopid": t.ShopID, "revoked_at": nil, "expires_at": bson.M{"$gt": time.Now()}})
	if err != nil {
		return err
	}
	if active >= mcpMaxTokens {
		return ErrTokenLimit
	}
	_, err = c.InsertOne(ctx, t)
	return err
}

func (m *MongoFeedback) ListTokens(ctx context.Context, shopID string) ([]MCPToken, error) {
	c, err := m.tokens(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := c.Find(ctx, bson.M{"shopid": shopID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	var out []MCPToken
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MongoFeedback) RevokeToken(ctx context.Context, shopID, id string, at time.Time) error {
	c, err := m.tokens(ctx)
	if err != nil {
		return err
	}
	res, err := c.UpdateOne(ctx, bson.M{"_id": id, "shopid": shopID, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": at}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrTokenInvalid
	}
	return nil
}

func (m *MongoFeedback) FindToken(ctx context.Context, hash string) (*MCPToken, error) {
	c, err := m.tokens(ctx)
	if err != nil {
		return nil, err
	}
	var t MCPToken
	if err := c.FindOne(ctx, bson.M{"hash": hash}).Decode(&t); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrTokenInvalid
		}
		return nil, err
	}
	return &t, nil
}

func (m *MongoFeedback) TouchToken(ctx context.Context, id string, at time.Time) error {
	c, err := m.tokens(ctx)
	if err != nil {
		return err
	}
	_, err = c.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"last_used_at": at}, "$inc": bson.M{"calls": 1}})
	return err
}

func (m *MongoFeedback) LogCall(ctx context.Context, call MCPCall) error {
	db, err := m.database(ctx)
	if err != nil {
		return err
	}
	_, err = db.Collection(mcpCallsColl).InsertOne(ctx, call)
	return err
}
