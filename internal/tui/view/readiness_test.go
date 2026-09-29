package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/news"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

func prereqNamed(items []prereq, prefix string) (prereq, bool) {
	for _, p := range items {
		if strings.HasPrefix(p.long, prefix) {
			return p, true
		}
	}
	return prereq{}, false
}

// fakeRun archive un entraînement dont le modèle de production couvre
// `symbols`, entraîné en `tf`, comme le walk-forward l'écrit.
func fakeRun(t *testing.T, modelsDir, strat, tf string, symbols []string) {
	t.Helper()
	root := filepath.Join(modelsDir, strat, "20260101-000000")
	final := filepath.Join(root, "final")
	if err := os.MkdirAll(final, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(final, strategy.ModelManifest), []byte("{}"), 0o644)
	run, _ := json.Marshal(map[string]any{
		"run_id": "20260101-000000", "strategy": strat, "symbols": symbols,
		"timeframe": tf, "final_model_dir": final, "started_at": "2026-01-01T00:00:00Z",
	})
	if err := os.WriteFile(filepath.Join(root, "run.json"), run, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPrerequisitesSayWhatIsMissingAndWhereToFixIt : une installation
// neuve n'a ni modèle ni historique. Le verdict le dit, et chaque point
// manquant renvoie à l'endroit où le réparer.
func TestPrerequisitesSayWhatIsMissingAndWhereToFixIt(t *testing.T) {
	deps := newTestDeps(t)
	cfg := deps.App.Config
	items := readiness(cfg, news.Status{})
	ok, missing := ready(items)
	if ok || !strings.HasPrefix(missing, "entraînement 0/") {
		t.Fatalf("installation neuve : entraînement manquant attendu, verdict %v %q", ok, missing)
	}
	train, _ := prereqNamed(items, "entraînement")
	if !strings.Contains(train.remedy, "gw backtrain") {
		t.Fatalf("le remède de l'entraînement doit renvoyer à gw backtrain : %+v", train)
	}
	if hist, _ := prereqNamed(items, "historique"); hist.state == checkOK || !strings.Contains(hist.remedy, "Données") {
		t.Fatalf("historique absent : %+v", hist)
	}

	// Entraîné, mais dans une autre unité que le live : toujours bloquant.
	cfg.Broker.Symbols = []string{"EURUSD"}
	fakeRun(t, cfg.Paths.ModelsDir(), cfg.Strategy.Name, "H1", []string{"EURUSD"})
	items = readiness(cfg, news.Status{})
	train, _ = prereqNamed(items, "entraînement")
	if train.state == checkOK || !strings.Contains(train.detail, "entraîné en H1, live en H4") {
		t.Fatalf("modèle H1 pour un live H4 : refus attendu, %+v", train)
	}
	cfg.Broker.Timeframe = "H1"
	items = readiness(cfg, news.Status{})
	if train, _ = prereqNamed(items, "entraînement"); train.state != checkOK {
		t.Fatalf("modèle H1 pour un live H1 : prérequis rempli attendu, %+v", train)
	}
}

// TestPrerequisitesRefuseAScalperOnH4 : Martinet n'a rien à faire en H4.
func TestPrerequisitesRefuseAScalperOnH4(t *testing.T) {
	deps := newTestDeps(t)
	cfg := deps.App.Config
	cfg.Strategy.Name = "martinet_v1_1"
	items := readiness(cfg, news.Status{})
	unit, _ := prereqNamed(items, "unité")
	if unit.state != checkKO || !strings.Contains(unit.detail, "M1, M5, M15") {
		t.Fatalf("martinet en H4 : unité refusée attendue, %+v", unit)
	}
	if _, ok := prereqNamed(items, "calendrier"); !ok {
		t.Fatal("martinet déclare les actualités : le calendrier doit figurer aux prérequis")
	}
	cfg.Broker.Timeframe = "M5"
	if unit, _ = prereqNamed(readiness(cfg, news.Status{}), "unité"); unit.state != checkOK {
		t.Fatalf("martinet en M5 : unité acceptée attendue, %+v", unit)
	}
}

// TestLiveOpensOnThePrerequisitesWhenSomethingIsMissing : l'écran Live
// d'une installation neuve s'ouvre sur le détail des prérequis ; « p »
// revient aux paires, et la ligne des prérequis reste visible.
func TestLiveOpensOnThePrerequisitesWhenSomethingIsMissing(t *testing.T) {
	deps := newTestDeps(t)
	v := NewLive(deps).(*Live)
	v.Init()
	out := v.Render(160, 40)
	for _, want := range []string{"Prérequis de la séance", "✗ entraînement", "→ gw backtrain", "→ manque : entraînement"} {
		if !strings.Contains(out, want) {
			t.Fatalf("« %s » absent de l'écran Live :\n%s", want, out)
		}
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	out = v.Render(160, 40)
	if strings.Contains(out, "Prérequis de la séance") || !strings.Contains(out, "Paires tradables") ||
		!strings.Contains(out, "Prérequis") {
		t.Fatalf("« p » doit revenir aux paires en gardant la ligne des prérequis :\n%s", out)
	}
}
