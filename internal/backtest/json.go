package backtest

import (
	"encoding/json"
	"math"
)

// Le JSON standard ne sait pas représenter NaN ni ±Inf. Or trois
// métriques du projet peuvent légitimement valoir l'un ou l'autre :
//
//   - profit factor INFINI  : un jeu de trades sans aucune perte ;
//   - profit factor NaN     : aucun trade du tout ;
//   - Sharpe / SQN NaN      : variance nulle ou trop peu d'observations.
//
// Les écraser en zéro serait un mensonge (zéro est une mesure, pas une
// absence de mesure), et les refuser ferait échouer l'écriture de run.json.
// On les encode donc explicitement : null pour « non calculable »,
// "inf"/"-inf" pour un infini réel. La relecture rétablit la valeur
// exacte, si bien qu'un run archivé se relit à l'identique.
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return []byte("null"), nil
	case math.IsInf(v, 1):
		return []byte(`"inf"`), nil
	case math.IsInf(v, -1):
		return []byte(`"-inf"`), nil
	}
	return json.Marshal(v)
}

func (f *jsonFloat) UnmarshalJSON(raw []byte) error {
	switch string(raw) {
	case "null":
		*f = jsonFloat(math.NaN())
		return nil
	case `"inf"`:
		*f = jsonFloat(math.Inf(1))
		return nil
	case `"-inf"`:
		*f = jsonFloat(math.Inf(-1))
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*f = jsonFloat(v)
	return nil
}

// statsAlias sert à éviter la récursion infinie : un alias de type ne
// porte pas les méthodes de l'original.
type statsAlias Stats

// statsJSON réexpose les trois champs sensibles à une profondeur plus
// faible que ceux de la structure embarquée ; encoding/json donne la
// priorité au champ le moins profond, donc c'est celui-ci qui est écrit.
type statsJSON struct {
	statsAlias
	ProfitFactor jsonFloat `json:"profit_factor"`
	Sharpe       jsonFloat `json:"sharpe"`
	SQN          jsonFloat `json:"sqn"`
}

// MarshalJSON sérialise les statistiques sans perdre les valeurs non
// finies.
func (s Stats) MarshalJSON() ([]byte, error) {
	return json.Marshal(statsJSON{
		statsAlias:   statsAlias(s),
		ProfitFactor: jsonFloat(s.ProfitFactor),
		Sharpe:       jsonFloat(s.Sharpe),
		SQN:          jsonFloat(s.SQN),
	})
}

// UnmarshalJSON relit des statistiques écrites par MarshalJSON.
func (s *Stats) UnmarshalJSON(raw []byte) error {
	var tmp statsJSON
	if err := json.Unmarshal(raw, &tmp); err != nil {
		return err
	}
	*s = Stats(tmp.statsAlias)
	s.ProfitFactor = float64(tmp.ProfitFactor)
	s.Sharpe = float64(tmp.Sharpe)
	s.SQN = float64(tmp.SQN)
	return nil
}
