package cli

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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

// portDocs names, for every interface of the core ports, the fake and the
// contract suite that adding-an-adapter.md must mention.
var portDocs = map[string][]string{
	"UsageSource": {"StaticSource", "UsageSourceContract"},
	"Store":       {"MemStore", "StoreContract"},
	"PriceBook":   {"TablePrices", "PriceBookContract"},
	"Clock":       {"FixedClock"},
	"Journal":     {"MemJournal", "JournalContract"},
	"RunJournal":  {"MemRunJournal", "RunJournalContract"},
	"Sandbox":     {"FakeSandbox", "SandboxContract"},
	"Box":         {"FakeSandbox"},
	"Workspace":   {"FakeWorkspace", "WorkspaceContract"},
	"Artifacts":   {"MemArtifacts", "ArtifactsContract"},
	"AgentRunner": {"ScriptedAgent", "AgentRunnerContract"},
}

func TestEveryPortIsDocumented(t *testing.T) {
	doc := readDoc(t, "adding-an-adapter.md")
	fset := token.NewFileSet()
	for _, file := range []string{"ports.go", "delegate_ports.go"} {
		f, err := parser.ParseFile(fset, filepath.Join("..", "core", file), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, isIface := ts.Type.(*ast.InterfaceType); !isIface {
					continue
				}
				names, known := portDocs[ts.Name.Name]
				if !known {
					t.Errorf("port %s has no entry in portDocs: add its fake and contract", ts.Name.Name)
					continue
				}
				for _, n := range append([]string{ts.Name.Name}, names...) {
					if !strings.Contains(doc, "`"+n) {
						t.Errorf("docs/adding-an-adapter.md does not mention `%s` (port %s)", n, ts.Name.Name)
					}
				}
			}
		}
	}
}

var securityTestRe = regexp.MustCompile("`([a-z]+)\\.(Test[A-Za-z0-9_]+)`")

// TestSecurityTestsExist: every test named in docs/security.md exists in the
// package it is attributed to (integration-tagged tests included).
func TestSecurityTestsExist(t *testing.T) {
	doc := readDoc(t, "security.md")
	cache := map[string]string{}
	source := func(pkg string) string {
		if s, ok := cache[pkg]; ok {
			return s
		}
		var all strings.Builder
		files, _ := filepath.Glob(filepath.Join("..", pkg, "*_test.go"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			all.Write(b)
		}
		cache[pkg] = all.String()
		return cache[pkg]
	}
	matches := securityTestRe.FindAllStringSubmatch(doc, -1)
	if len(matches) < 50 {
		t.Fatalf("found only %d test references in docs/security.md", len(matches))
	}
	for _, m := range matches {
		if !strings.Contains(source(m[1]), "func "+m[2]+"(") {
			t.Errorf("docs/security.md names %s.%s, which does not exist", m[1], m[2])
		}
	}
}
