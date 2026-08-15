package translation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// promptVersion invalidates cached translations when the prompt wording or
// normalization rules change.
const promptVersion = 1

// maxCacheEntries bounds the standalone cache file; least-recently-used rows
// are evicted when the cap is exceeded.
const maxCacheEntries = 10000

// CachePath keeps the translation cache outside the core catalog database so
// the feature stays isolated and disposable: deleting this file only loses
// cached translations.
func CachePath(home string) string {
	return filepath.Join(home, "mmd", "translation-cache.db")
}

type Cache struct {
	db     *sql.DB
	stores int
}

func OpenCache(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		CREATE TABLE IF NOT EXISTS translation_cache (
			cache_key TEXT PRIMARY KEY,
			source_sha256 TEXT NOT NULL,
			target_language TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			source_preview TEXT NOT NULL DEFAULT '',
			translation TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			used_at INTEGER NOT NULL,
			use_count INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS translation_cache_used ON translation_cache(used_at);
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	cache := &Cache{db: db}
	cache.evict(context.Background())
	return cache, nil
}

func (c *Cache) Close() error { return c.db.Close() }

// normalizeSource must match the whitespace handling in the Miru frontend
// (`text.replace(/\s+/g, ' ').trim()`) so the same rendered paragraph always
// maps to the same key.
func normalizeSource(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// Key identifies one translation uniquely by content, target, model, and
// prompt version. The provider and model are part of the key so switching
// models never replays another model's output.
func Key(request Request, provider, model string) (key, normalized string) {
	normalized = normalizeSource(request.Text)
	target := request.TargetLanguage
	if strings.TrimSpace(target) == "" {
		target = DefaultTargetLanguage
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v%d\n%s\n%s\n%s\n%s", promptVersion, provider, model, target, normalized)))
	return hex.EncodeToString(sum[:]), normalized
}

func (c *Cache) Get(ctx context.Context, key string) (string, bool, error) {
	var translation string
	err := c.db.QueryRowContext(ctx,
		`SELECT translation FROM translation_cache WHERE cache_key = ?`, key).Scan(&translation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	_, _ = c.db.ExecContext(ctx,
		`UPDATE translation_cache SET used_at = ?, use_count = use_count + 1 WHERE cache_key = ?`,
		time.Now().Unix(), key)
	return translation, true, nil
}

func (c *Cache) Store(ctx context.Context, key string, request Request, provider, model, translated string) error {
	if strings.TrimSpace(translated) == "" {
		return errors.New("refusing to cache an empty translation")
	}
	normalized := normalizeSource(request.Text)
	sourceSum := sha256.Sum256([]byte(normalized))
	target := request.TargetLanguage
	if strings.TrimSpace(target) == "" {
		target = DefaultTargetLanguage
	}
	preview := []rune(normalized)
	if len(preview) > 120 {
		preview = preview[:120]
	}
	now := time.Now().Unix()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO translation_cache (cache_key, source_sha256, target_language, provider, model, source_preview, translation, created_at, used_at, use_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(cache_key) DO UPDATE SET
			translation = excluded.translation,
			used_at = excluded.used_at`,
		key, hex.EncodeToString(sourceSum[:]), target, provider, model, string(preview), translated, now, now)
	if err != nil {
		return err
	}
	c.stores++
	if c.stores%64 == 0 {
		c.evict(ctx)
	}
	return nil
}

func (c *Cache) evict(ctx context.Context) {
	_, _ = c.db.ExecContext(ctx, `
		DELETE FROM translation_cache WHERE cache_key NOT IN (
			SELECT cache_key FROM translation_cache ORDER BY used_at DESC LIMIT ?
		)`, maxCacheEntries)
}

// CachedStreamer serves repeated paragraphs from the cache and only invokes
// the inner Pi-backed streamer on a miss. The NDJSON event shape is identical
// either way, so Miru cannot tell a replay from a live generation.
type CachedStreamer struct {
	Cache    *Cache // nil disables caching
	Inner    Streamer
	Provider string
	Model    string
}

func (c CachedStreamer) Stream(ctx context.Context, request Request, emit EmitFunc) error {
	provider, model := c.Provider, c.Model
	if provider == "" {
		provider = DefaultProvider
	}
	if model == "" {
		model = DefaultModel
	}
	if c.Cache == nil {
		return c.Inner.Stream(ctx, request, emit)
	}
	key, normalized := Key(request, provider, model)
	if hit, ok, err := c.Cache.Get(ctx, key); err == nil && ok {
		if err := emit(Event{Type: "start", ID: request.ID, Provider: provider, Model: model}); err != nil {
			return err
		}
		if err := emit(Event{Type: "delta", ID: request.ID, Text: hit}); err != nil {
			return err
		}
		return emit(Event{Type: "done", ID: request.ID})
	}

	request.Text = normalized
	var accumulated strings.Builder
	err := c.Inner.Stream(ctx, request, func(event Event) error {
		if event.Type == "delta" {
			accumulated.WriteString(event.Text)
		}
		return emit(event)
	})
	if err != nil {
		return err
	}
	// A failed store must never fail the user-visible translation.
	_ = c.Cache.Store(ctx, key, request, provider, model, accumulated.String())
	return nil
}
