// Command shopbrain builds the per-shop brain (master data + booking patterns
// learned from saved journals) and writes it as brain.json and/or an
// Obsidian-style markdown vault. It only reads MongoDB.
//
//	MONGO_URI=... MONGO_DB_NAME=... go run ./cmd/shopbrain -shop <id>[,<id>...] -out ./shopbrain-out
//
// MONGO_URI / MONGO_DB_NAME come from the environment only; this command does
// not load .env, so it is always explicit which database it reads.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/configs"
	"github.com/bosocmputer/account_ocr_gemini/internal/shopbrain"
	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
	"github.com/gin-gonic/gin"
)

func main() {
	shops := flag.String("shop", "", "shopid, or several separated by commas (required)")
	out := flag.String("out", "shopbrain-out", "output directory; each shop gets <out>/<shopid>/")
	format := flag.String("format", "both", "json | markdown | both")
	prev := flag.String("prev", "", "previous brain.json to diff against (single shop), or a directory holding <shopid>/brain.json")
	halfLife := flag.Float64("halflife", 180, "recency half-life in days for pattern weights")
	def := shopbrain.DefaultOptions(time.Now())
	minCons := flag.Float64("min-consistency", def.MinConsistency, "only predict from a key whose leading shape has at least this share (L1–L3)")
	minConsFmt := flag.Float64("min-consistency-format", def.MinConsistencyFormat, "same bar for L4 (docformat + book)")
	serve := flag.String("serve", "", "instead of writing files, serve only the shop-brain HTTP routes on this address (e.g. :18299) for testing; needs MAIN_API_URL")
	flag.Parse()

	if *serve == "" && *shops == "" {
		flag.Usage()
		os.Exit(2)
	}
	configs.MONGO_URI = os.Getenv("MONGO_URI")
	configs.MONGO_DB_NAME = os.Getenv("MONGO_DB_NAME")
	if configs.MONGO_URI == "" || configs.MONGO_DB_NAME == "" {
		log.Fatal("set MONGO_URI and MONGO_DB_NAME in the environment")
	}
	if err := storage.InitMongoDB(); err != nil {
		log.Fatal(err)
	}
	defer storage.CloseMongoDB()

	if *serve != "" {
		serveBrain(*serve)
		return
	}

	ids := strings.Split(*shops, ",")
	failed := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := run(id, *out, *format, *prev, *halfLife, *minCons, *minConsFmt, len(ids) > 1); err != nil {
			log.Printf("❌ %s: %v", id, err)
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func run(shopID, outDir, format, prevPath string, halfLife, minCons, minConsFmt float64, many bool) error {
	start := time.Now()
	ctx := context.Background()
	master, errsM, err := shopbrain.LoadMaster(ctx, shopID)
	if err != nil {
		return err
	}
	journals, groups, errsJ, err := shopbrain.LoadJournals(ctx, shopID)
	if err != nil {
		return err
	}
	opts := shopbrain.DefaultOptions(time.Now())
	opts.HalfLifeDays = halfLife
	opts.MinConsistency, opts.MinConsistencyFormat = minCons, minConsFmt
	b := shopbrain.Build(master, journals, groups, errsM+errsJ, opts)

	if prevPath != "" {
		p := prevPath
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			p = filepath.Join(p, shopID, "brain.json")
		} else if many {
			return fmt.Errorf("-prev must be a directory when several shops are given")
		}
		prev, err := readBrain(p)
		if err != nil {
			return fmt.Errorf("read -prev: %w", err)
		}
		if prev.Master.Shop.ShopID != shopID {
			return fmt.Errorf("-prev is for shop %s, not %s", prev.Master.Shop.ShopID, shopID)
		}
		b.Diff = shopbrain.Compare(prev, b).Capped()
	}

	dir := filepath.Join(outDir, shopID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if format == "json" || format == "both" {
		data, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "brain.json"), data, 0o644); err != nil {
			return err
		}
	}
	if format == "markdown" || format == "both" {
		if err := shopbrain.WriteVault(b, filepath.Join(dir, "vault")); err != nil {
			return err
		}
	}

	r, o, bl := b.Readiness, b.Backtest.Overall, b.Backtest.BaselinePartyDocformat
	log.Printf("✅ %s %s | journals %d (image %d, no_image %d, statement %d) groups %d decode-errors %d | patterns %d | backtest: predicted %d/%d acc %.0f%% full %.0f%% | baseline party+docformat: %d predicted acc %.0f%% full %.0f%% | insufficient=%t | %s",
		shopID, master.Shop.Name, r.Journals, r.BySource[shopbrain.SourceImage], r.BySource[shopbrain.SourceNoImage], r.BySource[shopbrain.SourceStatement],
		len(groups), r.DecodeErrors, len(b.Patterns), o.Predicted, o.Total, o.RateAcc*100, o.RateFull*100,
		bl.Predicted, bl.RateAcc*100, bl.RateFull*100, r.Insufficient, time.Since(start).Round(time.Millisecond))
	if b.Diff != nil {
		t := b.Diff.Totals
		log.Printf("   diff vs %s: added %d changed %d removed %d | patterns changed %d new %d gone %d",
			b.Diff.PrevGeneratedAt.Format(time.RFC3339), t.Added, t.Changed, t.Removed, t.PatternsChanged, t.PatternsNew, t.PatternsGone)
	}
	return nil
}

func readBrain(path string) (*shopbrain.Brain, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b shopbrain.Brain
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// serveBrain runs just the shop-brain routes (same handlers as cmd/api, under
// the same /billscan prefix) without the rest of the API — notably without
// batchocr.StartResume — so it can be tried against a real database safely.
func serveBrain(addr string) {
	configs.MAIN_API_URL = os.Getenv("MAIN_API_URL")
	if configs.MAIN_API_URL == "" {
		log.Fatal("set MAIN_API_URL for -serve")
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	h := &shopbrain.Handlers{Svc: shopbrain.NewService(shopbrain.NewOwnerVerifier(configs.MAIN_API_URL))}
	r.GET("/billscan/api/v1/shop-brain", h.GetShopBrain)
	r.GET("/billscan/api/v1/shop-brain/export", h.ExportShopBrain)
	log.Printf("serving shop-brain routes on %s (main API %s)", addr, configs.MAIN_API_URL)
	log.Fatal(r.Run(addr))
}
