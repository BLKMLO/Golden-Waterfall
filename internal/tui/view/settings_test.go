package view

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
)

func newSettings(t *testing.T) *Settings {
	t.Helper()
	return NewSettings(newTestDeps(t)).(*Settings)
}

func (v *Settings) findField(t *testing.T, path string) int {
	t.Helper()
	for i, f := range v.fields {
		if f.Path == path {
			return i
		}
	}
	t.Fatalf("réglage %q absent de l'écran", path)
	return -1
}

// TestEverySettingRoundTrips : un réglage qui se lit mais ne se réécrit
// pas identique perdrait la valeur de l'utilisateur au premier passage.
func TestEverySettingRoundTrips(t *testing.T) {
	v := newSettings(t)
	for _, f := range v.fields {
		before := f.Get(&v.draft)
		if err := f.Set(&v.draft, before); err != nil {
			t.Errorf("%s : relire sa propre valeur échoue (%v)", f.Path, err)
			continue
		}
		if after := f.Get(&v.draft); after != before {
			t.Errorf("%s : %q relu devient %q", f.Path, before, after)
		}
		if strings.TrimSpace(f.Help) == "" {
			t.Errorf("%s : aucune explication — un réglage sans phrase est un réglage mal réglé", f.Path)
		}
	}
}

// TestSettingsCoverTheAccountAndRisk : l'écran doit exposer de quoi
// configurer un compte, sinon il faut éditer le YAML à la main.
func TestSettingsCoverTheAccountAndRisk(t *testing.T) {
	v := newSettings(t)
	for _, path := range []string{
		"broker.name", "broker.mode", "broker.host", "broker.port",
		"backtest.account_currency", "backtest.initial_capital", "backtest.leverage",
		"risk.max_position_size", "risk.risk_per_trade_pct", "risk.max_daily_loss_pct",
		"strategy.name", "history.instruments", "training.folds", "logging.level",
	} {
		v.findField(t, path)
	}
}

// TestLiveModeNeedsConfirmation : basculer en argent réel ne doit jamais
// tenir en une frappe distraite.
func TestLiveModeNeedsConfirmation(t *testing.T) {
	v := newSettings(t)
	v.cursor = v.findField(t, "broker.mode")
	f := v.current()

	v.apply(f, "live")
	if got := f.Get(&v.draft); got == "live" {
		t.Fatal("le passage en live a été accepté sans confirmation")
	}
	v.apply(f, "live") // seconde frappe : confirmation
	if got := f.Get(&v.draft); got != "live" {
		t.Fatalf("après confirmation, le mode vaut %q", got)
	}
}

// TestForcedByEnvIsNotEditable : l'environnement a le dernier mot au
// démarrage. Proposer de modifier un réglage qu'il réécrira serait un
// piège — exactement celui que la règle est censée éviter.
func TestForcedByEnvIsNotEditable(t *testing.T) {
	t.Setenv("GW_MODE", "paper")
	v := newSettings(t)
	v.cursor = v.findField(t, "broker.mode")
	before := v.current().Get(&v.draft)

	v.step(1)
	if after := v.current().Get(&v.draft); after != before {
		t.Fatalf("réglage forcé par GW_MODE modifié quand même : %q → %q", before, after)
	}
	if !strings.Contains(v.Render(100, 30), "GW_MODE") {
		t.Fatal("l'écran doit DIRE quelle variable force le réglage")
	}
}

// TestSaveRefusesAnInvalidConfig : écrire un fichier que le programme
// refuserait de relire transformerait un réglage maladroit en démarrage
// impossible.
func TestSaveRefusesAnInvalidConfig(t *testing.T) {
	v := newSettings(t)
	file := v.deps.App.Config.Paths.ConfigFile()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	v.cursor = v.findField(t, "risk.max_open_positions")
	if err := v.current().Set(&v.draft, "0"); err != nil { // < 1 : refusé
		t.Fatal(err)
	}
	v.save()

	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("config.yaml a été réécrit avec une configuration invalide")
	}
	if v.invalid == "" {
		t.Fatal("la raison du refus doit être affichée")
	}
}

// TestSaveWritesAReadableFile : le fichier écrit doit se relire par le
// chemin NORMAL du démarrage, validation comprise.
func TestSaveWritesAReadableFile(t *testing.T) {
	v := newSettings(t)
	v.cursor = v.findField(t, "risk.max_open_positions")
	if err := v.current().Set(&v.draft, "6"); err != nil {
		t.Fatal(err)
	}
	v.save()

	back, err := config.Load(v.deps.App.Config.Paths)
	if err != nil {
		t.Fatalf("le fichier écrit ne se relit pas : %v", err)
	}
	if back.Risk.MaxOpenPositions != 6 {
		t.Fatalf("valeur perdue : %d", back.Risk.MaxOpenPositions)
	}
}

// TestEditingCapturesKeys : pendant une saisie, un chiffre appartient au
// champ, pas au routeur d'onglets.
func TestEditingCapturesKeys(t *testing.T) {
	v := newSettings(t)
	if v.CapturesKeys() {
		t.Fatal("hors saisie, l'écran ne doit pas confisquer les touches")
	}
	v.cursor = v.findField(t, "broker.port")
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !v.CapturesKeys() {
		t.Fatal("en saisie, l'écran doit confisquer les touches")
	}
	v.buffer = ""
	for _, r := range "7496" {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := v.current().Get(&v.draft); got != "7496" {
		t.Fatalf("saisie perdue : %q", got)
	}
	if v.CapturesKeys() {
		t.Fatal("la saisie validée doit rendre les touches au routeur")
	}
}

// TestReloadDropsPendingEdits : « r » doit revenir à ce que le disque dit,
// pas à une moyenne des deux.
func TestReloadDropsPendingEdits(t *testing.T) {
	v := newSettings(t)
	v.cursor = v.findField(t, "risk.max_open_positions")
	v.apply(v.current(), "9")
	if !v.dirty {
		t.Fatal("une modification doit être signalée comme non enregistrée")
	}
	v.reload()
	if v.dirty {
		t.Fatal("après rechargement, plus rien n'est en attente")
	}
	if got := v.current().Get(&v.draft); got == "9" {
		t.Fatal("le rechargement a gardé la modification abandonnée")
	}
}
