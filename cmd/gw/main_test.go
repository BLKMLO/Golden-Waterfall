package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
)

// isolate redirige configuration et données vers un dossier jetable : une
// sous-commande ne doit JAMAIS écrire dans le dossier utilisateur réel
// pendant les tests.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GW_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("GW_DATA_DIR", filepath.Join(dir, "data"))
}

// capture récupère ce qu'une fonction écrit sur la sortie standard.
func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String(), runErr
}

func TestUnknownSubcommandFails(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"nawak"}) })
	if err == nil {
		t.Fatal("une sous-commande inconnue doit être une ERREUR, pas un silence")
	}
	if !strings.Contains(err.Error(), "nawak") {
		t.Fatalf("l'erreur doit nommer la sous-commande fautive : %v", err)
	}
	// On rappelle aussi ce qui existe : l'utilisateur s'est trompé, pas
	// l'inverse.
	if !strings.Contains(out, "gw backtest") {
		t.Fatal("l'aide doit être affichée quand la sous-commande est inconnue")
	}
}

func TestVersionIsPrinted(t *testing.T) {
	isolate(t)
	for _, arg := range []string{"version", "--version", "-v"} {
		out, err := capture(t, func() error { return run([]string{arg}) })
		if err != nil {
			t.Fatalf("%s : %v", arg, err)
		}
		if !strings.Contains(out, "Golden Waterfall") || !strings.Contains(out, Version) {
			t.Fatalf("%s affiche %q", arg, out)
		}
	}
}

func TestHelpListsEverySubcommand(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"download", "train", "backtest", "runs", "paths", "config", "version"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("l'aide ne mentionne pas « %s »", cmd)
		}
	}
}

// TestPathsHonoursEnvironment : les variables GW_* ont le dernier mot.
// Une variable exportée qui n'agirait pas serait un piège.
func TestPathsHonoursEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GW_CONFIG_DIR", filepath.Join(dir, "cfg"))
	t.Setenv("GW_DATA_DIR", filepath.Join(dir, "dat"))

	out, err := capture(t, func() error { return runPaths() })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Join(dir, "cfg")) || !strings.Contains(out, filepath.Join(dir, "dat")) {
		t.Fatalf("gw paths ignore GW_CONFIG_DIR / GW_DATA_DIR :\n%s", out)
	}
}

// TestConfigDefaultPrintsTheCommentedTemplate : `gw config --default` doit
// sortir le modèle EMBARQUÉ, y compris ses commentaires — c'est la seule
// documentation des réglages accessible sans réseau.
func TestConfigDefaultPrintsTheCommentedTemplate(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return runConfig([]string{"--default"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"broker:", "risk:", "max_position_size", "#"} {
		if !strings.Contains(out, want) {
			t.Errorf("le modèle de configuration ne contient pas %q", want)
		}
	}
}

// TestBacktestWithoutPairIsRefused : sans paire, la commande ne doit pas
// en choisir une au hasard.
func TestBacktestWithoutPairIsRefused(t *testing.T) {
	isolate(t)
	if _, err := capture(t, func() error { return runBacktest(nil) }); err == nil {
		t.Fatal("gw backtest sans paire doit échouer clairement")
	}
}

// TestPartitionArgsKeepsFlagsAfterPairs : « gw download EURUSD --year 2019 »
// doit limiter la période. Le paquet flag s'arrêtant au premier argument
// positionnel, une option placée APRÈS la paire serait sinon ignorée en
// silence — et le programme téléchargerait vingt ans au lieu d'un.
func TestPartitionArgsKeepsFlagsAfterPairs(t *testing.T) {
	takes := map[string]bool{"from": true, "to": true, "year": true}
	cases := []struct {
		args      []string
		wantFlags []string
		wantRest  []string
	}{
		{[]string{"EURUSD", "--year", "2019"}, []string{"--year", "2019"}, []string{"EURUSD"}},
		{[]string{"--year", "2019", "EURUSD"}, []string{"--year", "2019"}, []string{"EURUSD"}},
		{[]string{"EURUSD", "--from=2019", "--to=2020"}, []string{"--from=2019", "--to=2020"}, []string{"EURUSD"}},
		{[]string{"EURUSD", "GBPUSD"}, nil, []string{"EURUSD", "GBPUSD"}},
	}
	for _, c := range cases {
		flags, rest := partitionArgs(c.args, takes)
		if strings.Join(flags, " ") != strings.Join(c.wantFlags, " ") {
			t.Errorf("%v : options %v, %v attendues", c.args, flags, c.wantFlags)
		}
		if strings.Join(rest, " ") != strings.Join(c.wantRest, " ") {
			t.Errorf("%v : paires %v, %v attendues", c.args, rest, c.wantRest)
		}
	}
}

func TestHelpMentionsPartialDownload(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--year", "--from", "--to"} {
		if !strings.Contains(out, want) {
			t.Errorf("l'aide ne documente pas %s", want)
		}
	}
}

// TestHelpMentionsTheNewCommands : une sous-commande absente de l'aide
// n'existe pas pour l'utilisateur.
func TestHelpMentionsTheNewCommands(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"migrate", "import", "risk-per-trade", "PAIRE…"} {
		if !strings.Contains(out, want) {
			t.Errorf("l'aide ne mentionne pas « %s » :\n%s", want, out)
		}
	}
}

// TestTrainArgsSeparatePairsFromFlags : `gw train EURUSD --folds 3` doit
// marcher dans les deux ordres. Le paquet flag s'arrête au premier
// argument positionnel et ignorerait les options placées après.
func TestTrainArgsSeparatePairsFromFlags(t *testing.T) {
	takes := map[string]bool{"folds": true, "tf": true, "risk-per-trade": true}
	flags, pairs := partitionArgs(
		[]string{"EURUSD", "--folds", "3", "GBPUSD", "--risk-per-trade", "0"}, takes)
	if strings.Join(flags, " ") != "--folds 3 --risk-per-trade 0" {
		t.Fatalf("options : %v", flags)
	}
	if strings.Join(pairs, " ") != "EURUSD GBPUSD" {
		t.Fatalf("paires : %v", pairs)
	}
}

// TestSizingLabelSaysWhichRegime : annoncer « taille 100 000 » quand le
// dimensionnement au risque est actif annoncerait une quantité que le
// moteur ne prendra presque jamais.
func TestSizingLabelSaysWhichRegime(t *testing.T) {
	fixed := sizingLabel(config.RiskConfig{MaxPositionSize: 100000, FixedPositionSize: 10000})
	if !strings.Contains(fixed, "fixe") || !strings.Contains(fixed, "10000") {
		t.Fatalf("taille fixe : %q", fixed)
	}
	sized := sizingLabel(config.RiskConfig{
		MaxPositionSize: 100000, FixedPositionSize: 10000, RiskPerTradePct: 0.5})
	if !strings.Contains(sized, "0.50 %") || !strings.Contains(sized, "plafond") {
		t.Fatalf("risque par trade : %q", sized)
	}
	if strings.Contains(sized, "fixe") {
		t.Fatalf("le régime au risque ne doit pas parler de taille fixe : %q", sized)
	}
}

// TestMigrateSaysWhenThereIsNothingToDo : un silence, sur une commande
// qui touche des gigaoctets, se lit comme un échec.
func TestMigrateSaysWhenThereIsNothingToDo(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"migrate"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rien à convertir") {
		t.Fatalf("sortie : %q", out)
	}
}

// TestImportRequiresASymbol : sans paire, on ne saurait pas où ranger le
// fichier — et le deviner d'après son nom serait le meilleur moyen de
// verser de l'EURUSD dans du GBPUSD.
func TestImportRequiresASymbol(t *testing.T) {
	isolate(t)
	if _, err := capture(t, func() error { return run([]string{"import", "x.parquet"}) }); err == nil {
		t.Fatal("un import sans --symbol doit être refusé")
	}
}

// TestBacktestAcceptsFlagsAfterThePair : « gw backtest EURUSD --csv » est
// l'usage que le README documente. Le paquet flag s'arrêtant au premier
// argument positionnel, l'option était ignorée et la commande refusée.
func TestBacktestAcceptsFlagsAfterThePair(t *testing.T) {
	isolate(t)
	_, err := capture(t, func() error { return runBacktest([]string{"EURUSD", "--csv"}) })
	if err == nil {
		t.Skip("aucun modèle : la commande devait échouer plus loin")
	}
	if strings.Contains(err.Error(), "usage :") {
		t.Fatalf("l'option placée après la paire est ignorée : %v", err)
	}
}

// TestHelpStartsWithFirstSteps : quelqu'un qui découvre le programme lit
// le début de l'aide, pas la liste des options. Les premiers pas viennent
// donc AVANT tout le reste, dans l'ordre où ils se font.
func TestHelpStartsWithFirstSteps(t *testing.T) {
	isolate(t)
	out, err := capture(t, func() error { return run([]string{"--help"}) })
	if err != nil {
		t.Fatal(err)
	}
	first, commands := strings.Index(out, "PREMIERS PAS"), strings.Index(out, "COMMANDES")
	if first < 0 || commands < 0 || first > commands {
		t.Fatalf("les premiers pas doivent précéder les commandes :\n%s", out)
	}
	steps := []string{"gw download", "gw train", "gw backtest", "écran 1 Live"}
	last := first
	for _, s := range steps {
		i := strings.Index(out[first:commands], s)
		if i < 0 || first+i < last {
			t.Fatalf("étape « %s » absente ou hors d'ordre :\n%s", s, out[first:commands])
		}
		last = first + i
	}
}
