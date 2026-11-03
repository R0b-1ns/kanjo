// Package pricesfile is the prices.json adapter of core.PriceBook.
package pricesfile

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/R0b-1ns/kanjo/internal/core"
)

//go:embed prices.json
var defaultJSON []byte

const (
	maxFileBytes   = 1 << 20
	maxSourceBytes = 200
)

// prices is the five prices of a tier. Pointers make a missing price an error
// instead of a silent zero.
type prices struct {
	Input        *float64 `json:"input"`
	Output       *float64 `json:"output"`
	CacheWrite5m *float64 `json:"cache_write_5m"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
	CacheRead    *float64 `json:"cache_read"`
}

type longTier struct {
	AbovePromptTokens int64 `json:"above_prompt_tokens"`
	prices
}

type modelEntry struct {
	prices
	LongContext *longTier `json:"long_context"`
}

type file struct {
	Source  string                `json:"source"`
	Fetched string                `json:"fetched"`
	Unit    string                `json:"unit"`
	Models  map[string]modelEntry `json:"models"`
}

type entry struct {
	base      core.Price
	long      *core.Price
	threshold int64
}

// Book is a core.PriceBook read from a prices file.
type Book struct {
	source, fetched string
	models          map[string]entry
}

// Default returns the prices embedded in the binary.
func Default() *Book {
	b, err := Parse(defaultJSON)
	if err != nil {
		panic("kanjo: embedded prices.json is invalid: " + err.Error()) // caught by tests
	}
	return b
}

// Load reads a prices file; an empty path means the embedded prices.
func Load(path string) (*Book, error) {
	if path == "" {
		return Default(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("prices file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("prices file %s: %w", path, err)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("prices file %s is larger than %d bytes", path, maxFileBytes)
	}
	b, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("prices file %s: %w", path, err)
	}
	return b, nil
}

// Parse decodes and validates a prices file. Unknown fields, missing or
// negative prices and a malformed date are errors.
func Parse(data []byte) (*Book, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f file
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the JSON object")
	}
	if !validSource(f.Source) || !core.ValidDay(f.Fetched) || len(f.Models) == 0 {
		return nil, errors.New("source (printable text, at most 200 characters), fetched (YYYY-MM-DD) and models are required")
	}
	b := &Book{source: f.Source, fetched: f.Fetched, models: map[string]entry{}}
	for id, m := range f.Models {
		e, err := m.entry()
		if err != nil || !core.ValidModel(id) {
			return nil, fmt.Errorf("model %q: invalid id or prices", id)
		}
		b.models[id] = e
	}
	return b, nil
}

// validSource keeps the source safe to print on a terminal: no control or
// formatting characters, and a bounded length.
func validSource(s string) bool {
	return s != "" && len(s) <= maxSourceBytes && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

func (m modelEntry) entry() (entry, error) {
	base, err := m.prices.price()
	if err != nil {
		return entry{}, err
	}
	e := entry{base: base}
	if l := m.LongContext; l != nil {
		long, err := l.prices.price()
		if err != nil || l.AbovePromptTokens <= 0 {
			return entry{}, errors.New("invalid long_context tier")
		}
		e.long, e.threshold = &long, l.AbovePromptTokens
	}
	return e, nil
}

func (p prices) price() (core.Price, error) {
	var out core.Price
	for _, f := range []struct {
		in  *float64
		out *float64
	}{{p.Input, &out.Input}, {p.Output, &out.Output}, {p.CacheWrite5m, &out.CacheWrite5m}, {p.CacheWrite1h, &out.CacheWrite1h}, {p.CacheRead, &out.CacheRead}} {
		if f.in == nil || *f.in < 0 {
			return core.Price{}, errors.New("every price is required and must not be negative")
		}
		*f.out = *f.in
	}
	return out, nil
}

// Source is the URL the prices were taken from.
func (b *Book) Source() string { return b.source }

// Fetched is the date (YYYY-MM-DD) the prices were taken.
func (b *Book) Fetched() string { return b.fetched }

// Lookup implements core.PriceBook; a model without a long tier uses its base price.
func (b *Book) Lookup(model string, longContext bool) (core.Price, bool) {
	e, ok := b.models[model]
	if !ok {
		return core.Price{}, false
	}
	if longContext && e.long != nil {
		return *e.long, true
	}
	return e.base, true
}

// LongContext implements core.PriceBook.
func (b *Book) LongContext(model string, promptTokens int64) bool {
	e, ok := b.models[model]
	return ok && e.long != nil && promptTokens > e.threshold
}
