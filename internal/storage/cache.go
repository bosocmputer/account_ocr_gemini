// cache.go - In-memory cache for master data

package storage

import (
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// MasterDataCache stores frequently accessed master data
type MasterDataCache struct {
	Accounts     []bson.M
	JournalBooks []bson.M
	Creditors    []bson.M
	Debtors      []bson.M     // เพิ่มลูกหนี้
	ShopProfile  *ShopProfile // เพิ่มข้อมูลบริษัท
	LoadedAt     time.Time
	ShopID       string
	mu           sync.RWMutex
}

// Global cache map: shopID -> cache
var masterDataCacheMap = make(map[string]*MasterDataCache)
var cacheMutex sync.RWMutex

const CACHE_TTL = 5 * time.Minute // Cache expires after 5 minutes

// GetOrLoadMasterData retrieves master data from cache or loads from DB
func GetOrLoadMasterData(shopID string) (*MasterDataCache, error) {
	cacheMutex.RLock()
	cache, exists := masterDataCacheMap[shopID]
	cacheMutex.RUnlock()

	// Check if cache exists and is still valid
	if exists && time.Since(cache.LoadedAt) < CACHE_TTL {
		return cache, nil
	}

	// Cache expired or doesn't exist - load from DB
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	// Double-check after acquiring write lock
	cache, exists = masterDataCacheMap[shopID]
	if exists && time.Since(cache.LoadedAt) < CACHE_TTL {
		return cache, nil
	}

	// Load fresh data from MongoDB
	accounts, err := GetChartOfAccounts(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	journalBooks, err := GetJournalBooks(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	creditors, err := GetCreditors(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	debtors, err := GetDebtors(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	shopProfile, err := GetShopProfile(shopID)
	if err != nil {
		return nil, err
	}

	// Create new cache
	newCache := &MasterDataCache{
		Accounts:     accounts,
		JournalBooks: journalBooks,
		Creditors:    creditors,
		Debtors:      debtors,
		ShopProfile:  shopProfile,
		LoadedAt:     time.Now(),
		ShopID:       shopID,
	}

	masterDataCacheMap[shopID] = newCache
	return newCache, nil
}

// InvalidateCache removes cache for a specific shop
func InvalidateCache(shopID string) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	delete(masterDataCacheMap, shopID)
}

// ClearAllCache removes all cached data
func ClearAllCache() {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	masterDataCacheMap = make(map[string]*MasterDataCache)
}
