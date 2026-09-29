package live

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BLKMLO/Golden-Waterfall/internal/broker"
	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
)

// silentVolume : le rejeu, qui se déclare SANS volume (comme Interactive
// Brokers).
type silentVolume struct{ broker.Gateway }

func (g silentVolume) Info() broker.Info {
	i := g.Gateway.Info()
	i.Name, i.SuppliesVolume = "rejeu_sans_volume", false
	return i
}

// volumeModel : une stratégie dont le MODÈLE exige le volume, sans que sa
// Description le déclare (comme Martinet avec filtre de volume retenu).
type volumeModel struct{ loaded map[string]bool }

func (s *volumeModel) Describe() strategy.Description {
	return strategy.Description{Name: "test_modele_volume", ContextBars: 1}
}
func (s *volumeModel) Warmup(_ context.Context, req strategy.WarmupRequest) error {
	if req.ModelDir != "" {
		s.loaded[req.Symbol] = true
	}
	return nil
}
func (s *volumeModel) OnBar(_ context.Context, symbol string, _ core.Series, _ int) (core.Signal, error) {
	return strategy.NoSignal(symbol), nil
}
func (s *volumeModel) Shutdown() error                    { return nil }
func (s *volumeModel) Ready() (bool, string)              { return len(s.loaded) > 0, "aucun modèle" }
func (s *volumeModel) ModelUsesVolume(symbol string) bool { return s.loaded[symbol] }

func init() {
	strategy.Register("test_modele_volume", func() strategy.Strategy { return &volumeModel{loaded: map[string]bool{}} })
	broker.Register(broker.Info{Name: "rejeu_sans_volume", Label: "rejeu sans volume", Simulated: true, SupportsBracket: true},
		func(o broker.Options) (broker.Gateway, error) {
			g, err := broker.New("replay", o)
			if err != nil {
				return nil, err
			}
			return silentVolume{g}, nil
		})
}

// TestModelNeedingVolumeIsFlaggedOnAGatewayWithout : un modèle qui dépend
// du volume, sur une passerelle qui n'en publie pas, est signalé paire par
// paire — « ✗ volume » dans la liste de contrôle — au lieu d'un silence.
func TestModelNeedingVolumeIsFlaggedOnAGatewayWithout(t *testing.T) {
	for _, c := range []struct {
		gateway string
		wantOK  bool
	}{{"replay", true}, {"rejeu_sans_volume", false}} {
		rt, cfg, _ := setupRuntime(t)
		cfg.Strategy.Name = "test_modele_volume"
		cfg.Broker.Name = c.gateway
		rt.cfg = cfg
		final := filepath.Join(cfg.Paths.ModelsDir(), cfg.Strategy.Name, "20260101-000000", "final")
		os.MkdirAll(final, 0o755)
		os.WriteFile(filepath.Join(final, strategy.ModelManifest), []byte("{}"), 0o644)
		run, _ := json.Marshal(map[string]any{"run_id": "20260101-000000", "strategy": cfg.Strategy.Name,
			"symbols": []string{"EURUSD"}, "timeframe": cfg.Broker.Timeframe, "final_model_dir": final,
			"started_at": "2026-01-01T00:00:00Z"})
		os.WriteFile(filepath.Join(filepath.Dir(final), "run.json"), run, 0o644)

		if err := rt.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		snap := rt.Snapshot()
		rt.Disconnect()
		if len(snap.Symbols) != 1 {
			t.Fatalf("%s : %d paires", c.gateway, len(snap.Symbols))
		}
		s := snap.Symbols[0]
		if !s.ModelLoaded || !s.NeedsVolume || s.VolumeOK != c.wantOK {
			t.Fatalf("%s : modèle %v, besoin de volume %v, volume %v (attendu %v)",
				c.gateway, s.ModelLoaded, s.NeedsVolume, s.VolumeOK, c.wantOK)
		}
		if !c.wantOK && !strings.Contains(s.Notice, "volume") {
			t.Fatalf("%s : la paire doit dire pourquoi elle restera muette : %q", c.gateway, s.Notice)
		}
	}
}
