package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
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
	connectionURI := MONGO_URI

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
	mongoDB = client.Database(MONGO_DB_NAME)

	log.Println("✅ Connected to MongoDB successfully!")
	return nil
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
		return nil, fmt.Errorf("failed to query debtors: %w", err)
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
