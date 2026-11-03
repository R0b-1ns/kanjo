package pricesfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func TestPriceBookContract(t *testing.T) {
	kanjotest.PriceBookContract(t, Default(), "claude-opus-5-5")
}

func TestDefaultMatchesSpecTable(t *testing.T) {
	want := map[string]core.Price{ // spec section 7, USD per million tokens
		"claude-fable-5-1":  {Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
		"claude-opus-5-5":   {Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20},
		"claude-sonnet-5-5": {Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.10, Output: 10},
		"claude-haiku-5-5":  {Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50},
	}
	b := Default()
	for model, price := range want {
		if got, ok := b.Lookup(model, false); !ok || got != price {
			t.Errorf("%s = %+v (%v), want %+v", model, got, ok, price)
		}
	}
	long := core.Price{Input: 0.50, CacheWrite5m: 0.625, CacheWrite1h: 1, CacheRead: 0.05, Output: 2.50}
	if got, ok := b.Lookup("claude-haiku-5-5", true); !ok || got != long {
		t.Errorf("haiku long tier = %+v (%v), want %+v", got, ok, long)
	}
	if got, _ := b.Lookup("claude-opus-5-5", true); got != want["claude-opus-5-5"] {
		t.Errorf("a model without a long tier must fall back to its base price, got %+v", got)
	}
	if b.Source() != "https://platform.claude.com/docs/en/about-claude/pricing" || b.Fetched() != "2026-11-03" {
		t.Errorf("source %q fetched %q", b.Source(), b.Fetched())
	}
}

func TestLongContextThresholdIsExclusive(t *testing.T) {
	b := Default()
	for n, want := range map[int64]bool{0: false, 100000: false, 100001: true} {
		if got := b.LongContext("claude-haiku-5-5", n); got != want {
			t.Errorf("LongContext(haiku, %d) = %v, want %v", n, got, want)
		}
	}
	if b.LongContext("claude-opus-5-5", 1<<40) {
		t.Error("opus has no long-context tier")
	}
}

const okFile = `{"source":"https://example.test/p","fetched":"2026-11-03","unit":"USD per million tokens","models":{"m":{"input":1,"output":2,"cache_write_5m":3,"cache_write_1h":4,"cache_read":5}}}`

func TestParseAcceptsMinimalFile(t *testing.T) {
	b, err := Parse([]byte(okFile))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := b.Lookup("m", false); !ok || p.Output != 2 {
		t.Fatalf("price = %+v %v", p, ok)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":    strings.Replace(okFile, `"unit"`, `"extra":1,"unit"`, 1),
		"unknown in model": strings.Replace(okFile, `"input":1`, `"input":1,"bonus":9`, 1),
		"negative price":   strings.Replace(okFile, `"input":1`, `"input":-1`, 1),
		"missing price":    strings.Replace(okFile, `"cache_read":5`, `"x":5`, 1),
		"empty models":     `{"source":"s","fetched":"2026-11-03","unit":"u","models":{}}`,
		"bad date":         strings.Replace(okFile, "2026-11-03", "03/11/2026", 1),
		"no source":        strings.Replace(okFile, `"source":"https://example.test/p"`, `"source":""`, 1),
		"bad model id":     strings.Replace(okFile, `"m":{`, `"bad model":{`, 1),
		"not json":         `{`,
		"trailing data":    okFile + okFile,
		"zero threshold": strings.Replace(okFile, `"cache_read":5}`,
			`"cache_read":5,"long_context":{"above_prompt_tokens":0,"input":1,"output":2,"cache_write_5m":3,"cache_write_1h":4,"cache_read":5}}`, 1),
		"negative long price": strings.Replace(okFile, `"cache_read":5}`,
			`"cache_read":5,"long_context":{"above_prompt_tokens":10,"input":-1,"output":2,"cache_write_5m":3,"cache_write_1h":4,"cache_read":5}}`, 1),
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoad(t *testing.T) {
	if b, err := Load(""); err != nil || b.Fetched() != Default().Fetched() {
		t.Fatalf("Load(\"\") must return the embedded prices: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prices.json")
	if err := os.WriteFile(p, []byte(okFile), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Lookup("m", false); !ok {
		t.Fatal("custom file not used")
	}
	if _, err := Load(filepath.Join(dir, "absent.json")); err == nil || !strings.Contains(err.Error(), "absent.json") {
		t.Fatalf("missing file err = %v, want it to name the file", err)
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, []byte(strings.Repeat(" ", maxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err == nil {
		t.Fatal("an oversized prices file must be refused")
	}
}
