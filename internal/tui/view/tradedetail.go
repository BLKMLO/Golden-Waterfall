package view

import (
	"fmt"
	"strings"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/component"
	"github.com/BLKMLO/Golden-Waterfall/internal/tui/theme"
)

// renderTradeDetail : tout ce que le journal sait d'un trade, champ par
// champ. Les tableaux, étroits, n'en montrent qu'une partie ; rien n'est
// recalculé ici, seulement recopié.
func renderTradeDetail(th theme.Theme, t core.Trade, width, height int) string {
	side := th.Positive.Render("LONG")
	if t.Side != core.Buy {
		side = th.Negative.Render("SHORT")
	}
	pnl := th.Muted.Render(component.Dash)
	switch {
	case t.PnL > 0:
		pnl = th.Positive.Render(component.Money(t.PnL))
	case t.PnL < 0:
		pnl = th.Negative.Render(component.Money(t.PnL))
	}
	row := func(label, value string) string {
		return th.Muted.Render(component.Pad(label, 10)) + value
	}
	lines := []string{
		row("Paire", th.Accent.Render(t.Symbol)+"  "+side+"  "+component.Num(t.Quantity, 0)+" unités"),
		row("Entrée", component.Time(t.EntryTime)+"  "+component.Price(t.Symbol, t.EntryPrice)),
		row("Sortie", component.Time(t.ExitTime)+"  "+component.Price(t.Symbol, t.ExitPrice)),
		row("Durée", component.Duration(t.Duration())),
		row("Motif", component.Dash),
		row("P&L", pnl+th.Muted.Render("  net des coûts imputés")),
		row("Coûts", component.Num(t.Cost, 2)),
	}
	if t.ExitReason != "" {
		lines[4] = row("Motif", t.ExitReason)
	}
	if t.Strategy != "" {
		lines = append(lines, row("Stratégie", t.Strategy))
	}
	for i, l := range lines {
		lines[i] = component.Clip(l, component.PanelContent(width))
	}
	body := strings.Join(lines, "\n") + "\n" + th.Muted.Render("entrée ou échap : retour à la liste")
	title := "Trade"
	if t.ID > 0 {
		title = fmt.Sprintf("Trade #%d", t.ID)
	}
	return component.PanelH(th, title, body, width, height)
}
