package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
