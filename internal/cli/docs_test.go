package cli

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/config"
)

const docsDir = "../../docs"

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(docsDir, rel))
	if err != nil {
		t.Fatalf("missing documentation: %v", err)
	}
	return string(b)
}

func TestEveryCommandHasAPageDocumentingItsFlags(t *testing.T) {
	registers := map[string]func(*flag.FlagSet){
		"clean":       func(fs *flag.FlagSet) { cleanFlags(fs) },
		"finish":      func(fs *flag.FlagSet) { finishFlags(fs) },
		"mark":        func(fs *flag.FlagSet) { markFlags(fs) },
		"prices":      func(fs *flag.FlagSet) { pricesCheckFlags(fs) },
		"run":         func(fs *flag.FlagSet) { runFlags(fs) },
		"serve":       func(fs *flag.FlagSet) { serveFlags(fs) },
		"snapshot":    func(fs *flag.FlagSet) { snapshotFlags(fs) },
		"summary":     func(fs *flag.FlagSet) { summaryFlags(fs) },
		"usage":       func(fs *flag.FlagSet) { usageFlags(fs) },
		"self-update": func(fs *flag.FlagSet) { selfUpdateFlags(fs) },
		"version":     func(*flag.FlagSet) {},
	}
	for name := range commands {
		register, ok := registers[name]
		if !ok {
			t.Errorf("command %q has no flag registration in this test: add it", name)
			continue
		}
		page := name
		if name == "prices" {
			page = "prices-check" // a command with a subcommand: the page is named after it
		}
		doc := readDoc(t, "commands/"+page+".md")
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		register(fs)
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(doc, "`--"+f.Name+"`") {
				t.Errorf("docs/commands/%s.md does not document `--%s`", name, f.Name)
			}
		})
	}
}

func TestEveryConfigKeyIsDocumented(t *testing.T) {
	doc := readDoc(t, "configuration.md")
	for _, key := range tomlKeys(reflect.TypeOf(config.Config{}), "") {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("docs/configuration.md does not document `%s`", key)
		}
	}
}

// tomlKeys lists the dotted toml key of every leaf field.
func tomlKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("toml") == "-" {
			continue
		}
		key := prefix + f.Tag.Get("toml")
		if f.Type.Kind() == reflect.Struct {
			keys = append(keys, tomlKeys(f.Type, key+".")...)
		} else {
			keys = append(keys, key)
		}
	}
	return keys
}
