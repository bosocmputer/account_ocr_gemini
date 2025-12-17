package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var mongoClient *mongo.Client
var mongoDB *mongo.Database

// InitMongoDB initializes MongoDB connection
func InitMongoDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use connection URI from environment variable
	connectionURI := configs.MONGO_URI

	// Connect to MongoDB
	clientOptions := options.Client().ApplyURI(connectionURI)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Ping to verify connection
	err = client.Ping(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	mongoClient = client
	mongoDB = client.Database(configs.MONGO_DB_NAME)

	log.Println("✅ Connected to MongoDB successfully!")
	return nil
}

// GetMongoDB returns the MongoDB database instance
func GetMongoDB() *mongo.Database {
	return mongoDB
}

// CloseMongoDB closes MongoDB connection
func CloseMongoDB() {
	if mongoClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mongoClient.Disconnect(ctx)
		log.Println("MongoDB connection closed")
	}
}

// ShopName represents a single name entry in the names array
type ShopName struct {
	Code     string `bson:"code" json:"code"`
	Name     string `bson:"name" json:"name"`
	IsAuto   bool   `bson:"isauto" json:"isauto"`
	IsDelete bool   `bson:"isdelete" json:"isdelete"`
}

// ShopProfile represents a shop's profile information
type ShopProfile struct {
	GuidFixed      string     `bson:"guidfixed" json:"guidfixed"`
	Names          []ShopName `bson:"names" json:"names"`
	PromptShopInfo string     `bson:"promptshopinfo" json:"promptshopinfo"` // Custom prompt describing business type and context
	Settings       struct {
		TaxID string `bson:"taxid" json:"taxid"`
	} `bson:"settings" json:"settings"`
}

// GetCompanyName returns the Thai name (code="th") or first active name from Names array
func (s *ShopProfile) GetCompanyName() string {
	if s == nil || len(s.Names) == 0 {
		return ""
	}

	// Try to find Thai name first
	for _, n := range s.Names {
		if n.Code == "th" && !n.IsDelete && n.Name != "" {
			return n.Name
		}
	}

	// Fallback to first non-deleted name
	for _, n := range s.Names {
		if !n.IsDelete && n.Name != "" {
			return n.Name
		}
	}

	return ""
}

// GetShopProfile retrieves shop profile by shopid (guidfixed)
func GetShopProfile(shopID string) (*ShopProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	collection := mongoDB.Collection("shops")
	filter := bson.M{"guidfixed": shopID}

	var profile ShopProfile
	err := collection.FindOne(ctx, filter).Decode(&profile)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("shop profile not found for shopid: %s", shopID)
		}
		return nil, fmt.Errorf("failed to query shop profile: %w", err)
	}

	return &profile, nil
}

// GetChartOfAccounts retrieves chart of accounts from MongoDB filtered by shopid
func GetChartOfAccounts(shopID string, additionalFilter bson.M) ([]bson.M, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build filter with shopid
	filter := bson.M{"shopid": shopID}

	// Add additional filters if provided
	for k, v := range additionalFilter {
		filter[k] = v
	}

	collection := mongoDB.Collection("chartofaccounts")
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query chartofaccounts: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// GetJournalBooks retrieves journal books from MongoDB filtered by shopid
func GetJournalBooks(shopID string, additionalFilter bson.M) ([]bson.M, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build filter with shopid
	filter := bson.M{"shopid": shopID}

	// Add additional filters if provided
	for k, v := range additionalFilter {
		filter[k] = v
	}

	collection := mongoDB.Collection("journalBooks")
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query journalBooks: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// GetCreditors retrieves creditors from MongoDB filtered by shopid
func GetCreditors(shopID string, additionalFilter bson.M) ([]bson.M, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build filter with shopid
	filter := bson.M{"shopid": shopID}

	// Add additional filters if provided
	for k, v := range additionalFilter {
		filter[k] = v
	}

	collection := mongoDB.Collection("creditors")
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query creditors: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// GetDebtors retrieves debtors from MongoDB filtered by shopid
func GetDebtors(shopID string, additionalFilter bson.M) ([]bson.M, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build filter with shopid
	filter := bson.M{"shopid": shopID}

	// Add additional filters if provided
	for k, v := range additionalFilter {
		filter[k] = v
	}

	collection := mongoDB.Collection("debtors")
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		// Empty debtors is OK - some shops may not have debtors yet
		return []bson.M{}, nil
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err = cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	return results, nil
}

// --- Draft Management Functions ---

// ReceiptDraft represents a draft entry in MongoDB
type ReceiptDraft struct {
	DraftID         string                 `bson:"draft_id" json:"draft_id"`
	ShopID          string                 `bson:"shopid" json:"shopid"`
	ReceiptData     map[string]interface{} `bson:"receipt_data" json:"receipt_data"`
	AccountingEntry map[string]interface{} `bson:"accounting_entry" json:"accounting_entry"`
	AIAnalysis      map[string]interface{} `bson:"ai_analysis" json:"ai_analysis"`
	Validation      map[string]interface{} `bson:"validation" json:"validation"`
	Status          string                 `bson:"status" json:"status"`
	CreatedAt       time.Time              `bson:"created_at" json:"created_at"`
	ApprovedAt      *time.Time             `bson:"approved_at,omitempty" json:"approved_at,omitempty"`
	ApprovedBy      string                 `bson:"approved_by,omitempty" json:"approved_by,omitempty"`
	Modified        bool                   `bson:"modified" json:"modified"`
	ImageReference  map[string]interface{} `bson:"image_reference" json:"image_reference"`
}

// CreateDraft creates a new draft entry in MongoDB
func CreateDraft(draft ReceiptDraft) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	collection := mongoDB.Collection("receipt_drafts")
	_, err := collection.InsertOne(ctx, draft)
	if err != nil {
		return fmt.Errorf("failed to create draft: %w", err)
	}

	fmt.Printf("✓ Draft created: %s\n", draft.DraftID)
	return nil
}

// GetTemplateByID retrieves a single document template by guidfixed or ObjectID
func GetTemplateByID(shopID string, templateID string) (bson.M, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	collection := mongoDB.Collection("documentFormate")

	// Try to use as guidfixed first (more common use case)
	filter := bson.M{
		"guidfixed": templateID,
		"shopid":    shopID,
	}

	log.Printf("🔍 Querying template - Database: %s, Collection: %s, Filter: %+v", mongoDB.Name(), collection.Name(), filter)

	// Debug: Count total documents
	totalCount, _ := collection.CountDocuments(ctx, bson.M{})
	shopCount, _ := collection.CountDocuments(ctx, bson.M{"shopid": shopID})
	log.Printf("📊 Collection stats - Total: %d, For shopid '%s': %d", totalCount, shopID, shopCount)

	var template bson.M
	err := collection.FindOne(ctx, filter).Decode(&template)

	// If not found by guidfixed, try ObjectID
	if err == mongo.ErrNoDocuments {
		log.Printf("⚠️  Not found by guidfixed, trying ObjectID...")
		objectID, objErr := primitive.ObjectIDFromHex(templateID)
		if objErr == nil {
			filter = bson.M{
				"_id":    objectID,
				"shopid": shopID,
			}
			err = collection.FindOne(ctx, filter).Decode(&template)
		}
	}

	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("template not found with ID: %s", templateID)
		}
		return nil, fmt.Errorf("failed to query template: %w", err)
	}

	return template, nil
}
