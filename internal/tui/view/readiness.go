package view

import (
	"fmt"
	"strings"

	"github.com/BLKMLO/Golden-Waterfall/internal/config"
	"github.com/BLKMLO/Golden-Waterfall/internal/data"
	"github.com/BLKMLO/Golden-Waterfall/internal/news"
	"github.com/BLKMLO/Golden-Waterfall/internal/strategy"
	"github.com/BLKMLO/Golden-Waterfall/internal/training"
)

// prereq : un PRÉREQUIS d'une séance live — ce qui doit être fait AVANT
// de connecter, dans `gw backtrain` ou dans les Paramètres.
//
// La liste de contrôle par paire (checks) dit pourquoi une paire ne
// trade pas une fois la séance ouverte ; celle-ci dit, avant, si la
// séance a une chance de trader du tout, et où aller pour la réparer.
type prereq struct {
	long, short string
	state       checkState
	// advisory : informe sans bloquer le verdict (couverture partielle,
	// calendrier absent).
	advisory bool
	// detail : ce qui a été constaté ; remedy : où le réparer.
	detail, remedy string
}

// readiness évalue les prérequis de la configuration CHARGÉE. Tout est lu
// sur le disque (modèles, historique, archive d'actualités) : rien n'est
// supposé, et rien ne demande de connexion.
func readiness(cfg config.Config, newsStatus news.Status) []prereq {
	var out []prereq
	symbols := cfg.LiveSymbols()

	// 1. Moteur et unité de temps du live.
	strat, err := strategy.New(cfg.Strategy.Name)
	if err != nil {
		return []prereq{{long: "moteur", short: "moteur", state: checkKO,
			detail: err.Error(), remedy: "Paramètres : strategy.name"}}
	}
	desc := strat.Describe()
	strat.Shutdown()
	out = append(out, prereq{long: "moteur " + desc.Name, short: "moteur", state: checkOK,
		detail: desc.Summary})

	tf, tfErr := data.ParseTimeframe(cfg.Broker.Timeframe)
	if tfErr == nil {
		tfErr = strategy.CheckTimeframe(desc, tf)
	}
	unit := prereq{long: "unité " + cfg.Broker.Timeframe, short: cfg.Broker.Timeframe, state: checkOK,
		detail: "bougies live en " + cfg.Broker.Timeframe}
	if tfErr != nil {
		unit.state, unit.detail, unit.remedy = checkKO, tfErr.Error(), "Paramètres : broker.timeframe et training.timeframe"
	}
	out = append(out, unit)

	// 2. Entraînement : un modèle de production par paire suivie, dans
	// l'unité de temps du live.
	cov, covErr := training.Coverage(cfg.Paths.ModelsDir(), desc.Name, symbols)
	trained, wrongTF := 0, map[string][]string{}
	for _, sym := range symbols {
		run, ok := cov[sym]
		switch {
		case !ok:
		case run.Timeframe != "" && run.Timeframe != cfg.Broker.Timeframe:
			wrongTF[run.Timeframe] = append(wrongTF[run.Timeframe], sym)
		default:
			trained++
		}
	}
	train := prereq{long: fmt.Sprintf("entraînement %d/%d", trained, len(symbols)),
		short: fmt.Sprintf("entr. %d/%d", trained, len(symbols)), state: checkOK,
		remedy: "gw backtrain → Entraînement (p paires, r lancer)"}
	switch {
	case covErr != nil:
		train.state, train.detail = checkKO, "dossier des modèles illisible : "+covErr.Error()
	case trained == 0:
		train.state = checkKO
		train.detail = fmt.Sprintf("aucun modèle de production de %s pour les paires suivies", desc.Name)
	case trained < len(symbols):
		train.state, train.advisory = checkKO, true
		train.detail = fmt.Sprintf("%d paire(s) sans modèle : elles resteront muettes", len(symbols)-trained)
	default:
		train.detail = "un modèle de production par paire suivie"
		train.remedy = ""
	}
	for tfName, syms := range wrongTF {
		train.detail += fmt.Sprintf(" · entraîné en %s, live en %s : %s", tfName, cfg.Broker.Timeframe,
			strings.Join(syms, " "))
	}
	out = append(out, train)

	// 3. Historique local : il chauffe la stratégie à la connexion.
	inv, _ := data.Catalog(cfg.Paths.HistoryDir())
	have := map[string]bool{}
	for _, i := range inv {
		if len(i.Years) > 0 {
			have[i.Symbol] = true
		}
	}
	withHist := 0
	for _, sym := range symbols {
		if have[sym] {
			withHist++
		}
	}
	hist := prereq{long: fmt.Sprintf("historique %d/%d", withHist, len(symbols)),
		short: fmt.Sprintf("hist. %d/%d", withHist, len(symbols)), state: checkOK,
		detail: "historique local présent pour chaque paire suivie"}
	if withHist < len(symbols) {
		hist.state, hist.remedy = checkKO, "gw backtrain → Données (d la paire, D tout)"
		hist.detail = fmt.Sprintf("%d paire(s) sans historique : pas de contexte avant des heures de marché",
			len(symbols)-withHist)
		hist.advisory = withHist > 0
	}
	out = append(out, hist)

	// 4. Paramétrage du risque : des paires dimensionnables.
	sizing := prereq{long: "dimensionnement", short: "dim.", state: checkOK}
	if cfg.Risk.RiskPerTradePct > 0 {
		exact, _ := data.SplitByConversion(symbols, cfg.Backtest.AccountCurrency)
		sizing.long = fmt.Sprintf("dimensionnables %d/%d", len(exact), len(symbols))
		sizing.detail = fmt.Sprintf("%.2f %% de l'équité par trade, compte en %s",
			cfg.Risk.RiskPerTradePct, cfg.Backtest.AccountCurrency)
		if len(exact) == 0 {
			sizing.state = checkKO
			sizing.detail += " : aucune paire suivie n'a le " + cfg.Backtest.AccountCurrency + " pour base ou cotation"
			sizing.remedy = "Paramètres : broker.symbols, backtest.account_currency ou risk.risk_per_trade_pct"
		} else if len(exact) < len(symbols) {
			sizing.state, sizing.advisory = checkKO, true
			sizing.detail += fmt.Sprintf(" : %d paire(s) non dimensionnables, leurs entrées seront refusées",
				len(symbols)-len(exact))
			sizing.remedy = "Paramètres : backtest.account_currency, ou taille fixe (risk_per_trade_pct 0)"
		}
	} else {
		sizing.detail = fmt.Sprintf("taille fixe de %.0f unités", cfg.Risk.FixedPositionSize)
	}
	out = append(out, sizing)

	// 5. Calendrier économique, pour un moteur qui le déclare : sans
	// archive, le filtre ne filtre rien. Informe, ne bloque pas — en live
	// le flux est récupéré à la connexion.
	if desc.UsesNews && cfg.News.Enabled {
		cal := prereq{long: "calendrier", short: "news", state: checkOK, advisory: true,
			detail: newsStatus.Describe()}
		if newsStatus.Weeks == 0 {
			cal.state = checkKO
			cal.remedy = "gw news fetch (récupéré aussi à la connexion)"
		}
		out = append(out, cal)
	}
	return out
}

// ready : aucun prérequis BLOQUANT n'est manquant.
func ready(items []prereq) (bool, string) {
	for _, p := range items {
		if p.state != checkOK && !p.advisory {
			return false, p.long
		}
	}
	return true, ""
}
