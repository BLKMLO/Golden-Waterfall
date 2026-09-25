// Package strategytest est le banc de CONFORMITÉ commun à toutes les
// stratégies : ce que le contrat `strategy.Strategy` promet aux moteurs,
// vérifié sur n'importe quelle implémentation.
//
// Le catalogue (`internal/strategies`) le fait tourner sur CHAQUE stratégie
// enregistrée : une génération de moteur ajoutée au catalogue y passe
// d'office, sans que personne ait à s'en souvenir. C'est ce qui rend le
// remplacement de Colibri sans risque pour le reste du programme.
package strategytest

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// Series fabrique une série H4 synthétique au comportement APPRENABLE
// (retour à la moyenne autour d'une lente oscillation, plus du bruit),
// SANS bougie de week-end comme un vrai historique forex, et avec un côté
// ask. Elle ne sert qu'aux tests : jamais écrite chez l'utilisateur.
func Series(n int, seed int64, base float64) core.Series {
	rng := rand.New(rand.NewSource(seed))
	t := time.Date(2020, 1, 6, 0, 0, 0, 0, time.UTC) // un lundi
	out := make(core.Series, 0, n)
	price := base
	for i := 0; len(out) < n; i++ {
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
			t = t.Add(4 * time.Hour)
			continue
		}
		anchor := base + 0.02*base*math.Sin(float64(i)/90.0)
		open := price
		price = math.Max(price+(anchor-price)*0.05+rng.NormFloat64()*base*0.0012, base*0.5)
		high := math.Max(open, price) + math.Abs(rng.NormFloat64())*base*0.0004
		low := math.Min(open, price) - math.Abs(rng.NormFloat64())*base*0.0004
		spread := base * 0.0001
		out = append(out, core.Bar{
			Time:    t,
			BidOpen: open, BidHigh: high, BidLow: low, BidClose: price,
			AskOpen: open + spread, AskHigh: high + spread,
			AskLow: low + spread, AskClose: price + spread,
			Volume: 80 + rng.Float64()*60,
		})
		t = t.Add(4 * time.Hour)
	}
	return out
}

// Run soumet la stratégie `name` à toutes les vérifications du contrat.
func Run(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	s, err := strategy.New(name)
	if err != nil {
		t.Fatal(err)
	}
	desc := s.Describe()
	if desc.Name != name {
		t.Fatalf("Describe().Name = %q, enregistrée sous %q", desc.Name, name)
	}
	if desc.ContextBars < 0 || desc.MaxHold < 0 {
		t.Fatalf("contexte (%d) et horizon (%s) ne peuvent pas être négatifs", desc.ContextBars, desc.MaxHold)
	}

	series := Series(2400, 11, 1.10)
	if _, err := s.OnBar(ctx, "EURUSD", series, len(series)); err == nil {
		t.Fatal("un indice hors de la série doit être une erreur, pas un signal")
	}

	trainable, ok := s.(strategy.Trainable)
	if !ok {
		checkDecisions(t, s, series, desc)
		return
	}

	// --- Sans modèle : muette, et elle dit pourquoi -------------------------
	if err := s.Warmup(ctx, strategy.WarmupRequest{Symbol: "EURUSD", Series: series, Timeframe: data.H4}); err != nil {
		t.Fatalf("une chauffe sans modèle n'est pas une erreur : %v", err)
	}
	if ready, why := s.Ready(); ready || why == "" {
		t.Fatalf("sans modèle, la stratégie doit être non prête ET le dire (prête=%v, raison=%q)", ready, why)
	}
	if sig, err := s.OnBar(ctx, "EURUSD", series, len(series)-1); err != nil || sig.Action != core.Hold {
		t.Fatalf("sans modèle, aucun signal ne doit être improvisé : %v, %v", sig.Action, err)
	}
	if _, ok := trainable.ScoreOOS("EURUSD", series, 0); ok {
		t.Fatal("sans modèle, l'AUC out-of-sample n'est pas calculable : il faut le dire")
	}

	// --- Entraînement puis rechargement dans une instance NEUVE ------------
	datasets := map[string]core.Series{"EURUSD": series}
	pooled := false
	if p, ok := s.(strategy.Pooled); ok && p.PoolsSymbols() {
		pooled = true
		datasets["GBPUSD"] = Series(2400, 12, 1.27)
	}
	dir := t.TempDir()
	report, err := trainable.Train(ctx, strategy.TrainRequest{
		Datasets: datasets, Timeframe: data.H4, OutputDir: dir, Seed: 42, Threads: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples <= 0 || report.Features <= 0 {
		t.Fatalf("rapport d'entraînement vide : %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, strategy.ModelManifest)); err != nil {
		t.Fatalf("le manifeste %s est obligatoire : le catalogue ne reconnaîtrait pas ce modèle",
			strategy.ModelManifest)
	}
	if !pooled {
		// Mono-actif : plusieurs symboles doivent être refusés.
		if _, err := trainable.Train(ctx, strategy.TrainRequest{
			Datasets:  map[string]core.Series{"EURUSD": series, "GBPUSD": Series(2400, 12, 1.27)},
			Timeframe: data.H4, OutputDir: t.TempDir(), Seed: 1,
		}); err == nil {
			t.Fatal("une stratégie mono-actif doit refuser un entraînement mutualisé")
		}
	}

	fresh, _ := strategy.New(name)
	test := Series(1500, 99, 1.10)
	if err := fresh.Warmup(ctx, strategy.WarmupRequest{
		Symbol: "EURUSD", Series: test, Timeframe: data.H4, ModelDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	if ready, why := fresh.Ready(); !ready {
		t.Fatalf("modèle rechargé mais stratégie non prête : %s", why)
	}
	checkDecisions(t, fresh, test, desc)

	if auc, ok := fresh.(strategy.Trainable).ScoreOOS("EURUSD", test, desc.ContextBars); ok &&
		(auc < 0 || auc > 1 || math.IsNaN(auc)) {
		t.Fatalf("AUC hors de [0, 1] : %v", auc)
	}
}

// checkDecisions : chaque décision est causale (stabilité par préfixe),
// attribuée, et ses barrières sont du bon côté du prix.
func checkDecisions(t *testing.T, s strategy.Strategy, series core.Series, desc strategy.Description) {
	t.Helper()
	ctx := context.Background()
	entries := 0
	for i := desc.ContextBars; i < len(series); i++ {
		sig, err := s.OnBar(ctx, "EURUSD", series, i)
		if err != nil {
			t.Fatal(err)
		}
		if sig.Confidence < 0 || sig.Confidence > 1 || math.IsNaN(sig.Confidence) {
			t.Fatalf("bougie %d : confiance %v hors de [0, 1]", i, sig.Confidence)
		}
		if !sig.Action.IsEntry() {
			continue
		}
		entries++
		close := series[i].Close()
		if sig.Symbol != "EURUSD" || sig.Strategy != desc.Name || sig.Price != close {
			t.Fatalf("bougie %d : signal mal attribué %+v", i, sig)
		}
		// Stop obligatoire (sans lui, le risque ne peut pas dimensionner) ;
		// limite facultative — zéro veut dire « aucune », comme le contrat
		// de core.Signal le prévoit pour un suivi de tendance.
		long := sig.Action == core.EnterLong
		noLimit := sig.TakeProfit == 0
		if long && !(sig.StopLoss > 0 && sig.StopLoss < close && (noLimit || sig.TakeProfit > close)) ||
			!long && !(sig.StopLoss > close && (noLimit || sig.TakeProfit < close)) {
			t.Fatalf("bougie %d : barrières du mauvais côté pour %s : stop %v, limite %v, prix %v",
				i, sig.Action, sig.StopLoss, sig.TakeProfit, close)
		}
	}

	// Stabilité par PRÉFIXE des DÉCISIONS : décider à la bougie i avec ou
	// sans la suite de la série doit donner exactement le même signal. Une
	// stratégie qui regarderait l'avenir (une normalisation sur toute la
	// série, une médiane globale) échoue ici, même si chacune de ses
	// features est causale.
	step := (len(series) - desc.ContextBars) / 12
	if step < 1 {
		step = 1
	}
	for i := desc.ContextBars; i < len(series); i += step {
		full, err := s.OnBar(ctx, "EURUSD", series, i)
		if err != nil {
			t.Fatal(err)
		}
		prefix, err := s.OnBar(ctx, "EURUSD", series[:i+1], i)
		if err != nil {
			t.Fatal(err)
		}
		if full != prefix {
			t.Fatalf("bougie %d : la décision dépend des bougies FUTURES\n  série entière : %+v\n  préfixe       : %+v",
				i, full, prefix)
		}
	}
	t.Logf("%s : %d entrée(s) sur %d bougies décidées", desc.Name, entries, len(series)-desc.ContextBars)
}
