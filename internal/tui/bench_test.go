package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BLKMLO/Golden-Waterfall/internal/app"
	"github.com/BLKMLO/Golden-Waterfall/internal/config"
)

// newBenchApp : même montage que newTestApp, mais sur *testing.B.
func newBenchApp(b *testing.B) *app.App {
	b.Helper()
	root := b.TempDir()
	a, err := app.New(config.Paths{
		ConfigDir: filepath.Join(root, "cfg"),
		DataDir:   filepath.Join(root, "data"),
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { a.Close() })
	return a
}

// BenchmarkViewAllScreens mesure le coût d'une image complète.
//
// Le dessin est refait à chaque tick (500 ms par défaut), et depuis que
// les écrans MESURENT leurs panneaux au lieu de deviner leur hauteur, ils
// peuvent en rendre un deux fois. Mesuré : ≈ 0,6 ms par écran, soit un
// millième de la cadence de rafraîchissement. C'est le prix, et il est
// dérisoire devant un affichage faux.
func BenchmarkViewAllScreens(b *testing.B) {
	m := New(newBenchApp(b))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = model.(*Model)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range m.views {
			m.active = j
			_ = m.View()
		}
	}
}
