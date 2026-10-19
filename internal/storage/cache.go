// cache.go - In-memory cache for master data

package storage

import (
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// MasterDataCache stores frequently accessed master data
type MasterDataCache struct {
	Accounts          []bson.M
	JournalBooks      []bson.M
	AccountGroups     []bson.M // accountGroups collection — used by the Excel-import validation pipeline
	Creditors         []bson.M
	Debtors           []bson.M     // เพิ่มลูกหนี้
	ShopProfile       *ShopProfile // เพิ่มข้อมูลบริษัท
	DocumentTemplates []bson.M     // accounting templates (documentFormate collection)
	LoadedAt          time.Time
	ShopID            string
	mu                sync.RWMutex
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

	newCache, err := loadMasterData(shopID)
	if err != nil {
		return nil, err
	}

	masterDataCacheMap[shopID] = newCache
	return newCache, nil
}

// ForceReloadMasterData bypasses the TTL and re-reads the shop's master data from MongoDB, replacing the cached copy.
// Used where the user has typically just created the very record being looked up (e.g. a debtor added moments before an
// Excel import) and a stale cache would report it as "not found". On failure the existing cache is left untouched.
func ForceReloadMasterData(shopID string) (*MasterDataCache, error) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	newCache, err := loadMasterData(shopID)
	if err != nil {
		return nil, err
	}
	masterDataCacheMap[shopID] = newCache
	return newCache, nil
}

// loadMasterData reads every master-data collection for a shop straight from MongoDB.
// Any failure is returned as an error so a partial/empty result is never cached as if it were real.
func loadMasterData(shopID string) (*MasterDataCache, error) {
	accounts, err := GetChartOfAccounts(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	journalBooks, err := GetJournalBooks(shopID, bson.M{})
	if err != nil {
		return nil, err
	}

	accountGroups, err := GetAccountGroups(shopID, bson.M{})
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

	documentTemplates, err := GetDocumentFormate(shopID)
	if err != nil {
		return nil, err
	}

	return &MasterDataCache{
		Accounts:          accounts,
		JournalBooks:      journalBooks,
		AccountGroups:     accountGroups,
		Creditors:         creditors,
		Debtors:           debtors,
		ShopProfile:       shopProfile,
		DocumentTemplates: documentTemplates,
		LoadedAt:          time.Now(),
		ShopID:            shopID,
	}, nil
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
