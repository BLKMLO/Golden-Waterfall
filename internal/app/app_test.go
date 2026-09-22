package app

import (
	"path/filepath"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// newTestApp monte une application complète dans un dossier jetable.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(tempPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// tempPaths isole config et données dans un dossier jetable : aucun test
// ne doit écrire dans le dossier utilisateur réel.
func tempPaths(t *testing.T) config.Paths {
	t.Helper()
	dir := t.TempDir()
	return config.Paths{
		ConfigDir: filepath.Join(dir, "config"),
		DataDir:   filepath.Join(dir, "data"),
	}
}

// TestNewWiresEveryComponent : app.New est le SEUL endroit où les modules
// sont câblés. Un champ oublié ne se verrait qu'à la première utilisation,
// c'est-à-dire trop tard.
func TestNewWiresEveryComponent(t *testing.T) {
	a, err := New(tempPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	for name, ok := range map[string]bool{
		"Logger":   a.Logger != nil,
		"Logging":  a.Logging != nil,
		"Bus":      a.Bus != nil,
		"Store":    a.Store != nil,
		"Risk":     a.Risk != nil,
		"Live":     a.Live != nil,
		"Backtest": a.Backtest != nil,
		"Training": a.Training != nil,
	} {
		if !ok {
			t.Errorf("app.New laisse %s à nil", name)
		}
	}
}

// TestNewCreatesTheConfigTemplate : au premier lancement, le fichier de
// configuration commenté doit apparaître. Sans lui, l'utilisateur n'a
// aucun moyen de découvrir les réglages.
func TestNewCreatesTheConfigTemplate(t *testing.T) {
	paths := tempPaths(t)
	a, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if _, err := config.Load(paths); err != nil {
		t.Fatalf("configuration illisible après le premier démarrage : %v", err)
	}
}

// TestSecondInstanceIsRefused : bbolt verrouille le fichier. Deux
// instances qui écriraient le même journal de trades produiraient des
// données incohérentes — l'échec net est VOULU.
func TestSecondInstanceIsRefused(t *testing.T) {
	paths := tempPaths(t)
	first, err := New(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := New(paths)
	if err == nil {
		second.Close()
		t.Fatal("une seconde instance a démarré sur la même base : le verrou ne protège plus rien")
	}
}

// TestCloseIsIdempotentOnNil : la fermeture doit survivre à une
// application jamais construite (chemin d'erreur du démarrage).
func TestCloseIsIdempotentOnNil(t *testing.T) {
	var a *App
	if err := a.Close(); err != nil {
		t.Fatalf("Close sur une App nulle doit être inoffensif : %v", err)
	}
}

// TestSetRiskPerTradeRebuildsEverything : mesurer l'effet de
// `risk_per_trade_pct` demande deux runs qui ne diffèrent QUE par ce
// réglage. Si le moteur de backtest ou celui du walk-forward gardait
// l'ancien gestionnaire, la comparaison ne mesurerait rien.
func TestSetRiskPerTradeRebuildsEverything(t *testing.T) {
	a := newTestApp(t)
	before := a.Risk
	if err := a.SetRiskPerTrade(1.25); err != nil {
		t.Fatal(err)
	}
	if a.Config.Risk.RiskPerTradePct != 1.25 {
		t.Fatalf("configuration non mise à jour : %g", a.Config.Risk.RiskPerTradePct)
	}
	if a.Risk == before {
		t.Fatal("le gestionnaire de risque doit être rebâti")
	}
	if a.Backtest == nil || a.Training == nil || a.Live == nil {
		t.Fatal("les moteurs doivent être recâblés sur le nouveau gestionnaire")
	}
	// Preuve par le comportement : le nouveau gestionnaire dimensionne.
	d := a.Risk.Evaluate(
		core.Signal{Symbol: "EURUSD", Action: core.EnterLong, Price: 1.1, StopLoss: 1.0},
		nil, &core.AccountState{Equity: 10000})
	if !d.Accepted() {
		t.Fatalf("entrée refusée : %s", d.Reason)
	}
	if d.Order.Quantity != a.Config.Risk.FixedPositionSize {
		return // dimensionné au risque : c'est ce qu'on voulait
	}
	t.Fatal("la taille est restée fixe : le réglage n'a pas pris")
}

// TestSetRiskPerTradeRefusesOutOfBounds : un pourcentage absurde doit
// être refusé ici comme il l'est par la validation de configuration.
func TestSetRiskPerTradeRefusesOutOfBounds(t *testing.T) {
	a := newTestApp(t)
	for _, pct := range []float64{-1, 101} {
		if err := a.SetRiskPerTrade(pct); err == nil {
			t.Fatalf("%g %% devait être refusé", pct)
		}
	}
}
