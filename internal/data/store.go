// Package data gère les données de marché historiques : téléchargement
// depuis Dukascopy, stockage local, relecture et ré-échantillonnage.
//
// Le stockage est en **Parquet**, un format colonne standard. Le choix
// précédent — un format binaire maison, `.gwb` — était plus compact à
// écrire et se relisait par seek, mais il n'était lisible QUE par ce
// programme : impossible d'y verser un historique téléchargé ailleurs,
// impossible de l'ouvrir dans un tableur ou un notebook. Un format de
// données qui n'est lisible que par le logiciel qui l'a écrit enferme son
// utilisateur, et ce projet promet exactement l'inverse.
//
// Ce que la bascule a coûté et rapporté, mesuré sur une année de M1
// (372 000 bougies) : fichier 8,8 Mo au lieu de 14,9 ; lecture 64 ms au
// lieu de 32 ; écriture 330 ms au lieu de 68, ce qui ne se voit pas
// derrière un téléchargement réseau. Les .gwb existants restent LUS
// (gwb.go) et `gw migrate` les convertit.
package data

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// --- Organisation du dossier d'historique ---------------------------------

// FileHeader décrit un fichier d'une année pour un symbole.
type FileHeader struct {
	Symbol string
	Year   int
	// Scale : facteur de mise à l'échelle des prix (10^décimales). Il ne
	// sert plus qu'à l'ÉCRITURE, pour arrondir les prix à la précision
	// réelle de l'instrument avant de les confier au compresseur.
	Scale int32
	Count int64
	// Failures : nombre de jours que le téléchargement n'a PAS pu
	// récupérer (erreurs réseau, 5xx épuisant les tentatives). Les jours
	// de marché fermé n'en font pas partie : il n'y a rien à y télécharger.
	//
	// Ce champ existe pour une raison précise : sans lui, une année
	// écrite avec des trous était indiscernable d'une année complète, et
	// relancer le téléchargement la sautait — les trous devenaient
	// définitifs. Complete() est ce que le code doit interroger.
	Failures int32
	// Imported : le fichier n'a pas été écrit par Golden Waterfall. Son
	// nombre de jours manquants est alors INCONNU, pas nul — Complete()
	// n'est plus une mesure mais une supposition, et l'interface le dit
	// plutôt que de compter l'année comme faite.
	Imported bool
}

// Complete indique si l'année a été téléchargée sans aucun jour manquant.
//
// Un fichier importé ne peut pas répondre : personne n'a compté ses jours
// manquants. On répond « non » — ce qui déclenche un retéléchargement si
// l'utilisateur le demande, plutôt que de déclarer complète une année
// dont on ne sait rien.
func (h FileHeader) Complete() bool { return h.Failures == 0 && !h.Imported }

// --- Organisation du dossier d'historique ---------------------------------

// FilePath renvoie history/<SYMBOLE>/<SYMBOLE>_m1_<année>.parquet
func FilePath(historyDir, symbol string, year int) string {
	return filepath.Join(historyDir, symbol, fmt.Sprintf("%s_m1_%d%s", symbol, year, parquetExt))
}

// legacyFilePath renvoie le chemin de l'ANCIEN format pour la même année.
func legacyFilePath(historyDir, symbol string, year int) string {
	return filepath.Join(historyDir, symbol, fmt.Sprintf("%s_m1_%d%s", symbol, year, legacyExt))
}

// historyExt reconnaît un fichier d'historique et renvoie son extension.
func historyExt(name string) (string, bool) {
	for _, ext := range []string{parquetExt, legacyExt} {
		if strings.HasSuffix(name, ext) {
			return ext, true
		}
	}
	return "", false
}

// fileYear extrait l'année d'un nom de fichier, ou false si le suffixe
// n'en est pas une. Ces fichiers (copies manuelles, renommages) sont
// IGNORÉS, jamais chargés : mieux vaut un trou visible qu'un doublon.
func fileYear(name string) (int, bool) {
	base := filepath.Base(name)
	if ext, ok := historyExt(base); ok {
		base = strings.TrimSuffix(base, ext)
	}
	parts := strings.Split(base, "_")
	if len(parts) == 0 {
		return 0, false
	}
	y, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, false
	}
	return y, true
}

// symbolYearFromPath devine symbole et année d'après le chemin, pour un
// fichier importé dont les métadonnées ne disent rien.
func symbolYearFromPath(path string) (string, int) {
	year, _ := fileYear(path)
	symbol := ""
	base := filepath.Base(path)
	if ext, ok := historyExt(base); ok {
		base = strings.TrimSuffix(base, ext)
	}
	if parts := strings.Split(base, "_"); len(parts) > 1 {
		symbol = strings.ToUpper(parts[0])
	}
	if _, err := LookupInstrument(symbol); err != nil {
		// Le nom de fichier ne dit rien d'exploitable : le dossier parent
		// est la dernière piste, et c'est ainsi que l'arborescence est
		// organisée.
		if parent := filepath.Base(filepath.Dir(path)); parent != "." {
			symbol = strings.ToUpper(parent)
		}
	}
	return symbol, year
}

// yearFiles liste les fichiers d'historique d'un symbole, une entrée par
// année.
//
// Quand une année existe dans les DEUX formats — le cas pendant une
// migration — c'est le Parquet qui gagne : il porte les métadonnées, donc
// le compte des jours manquants.
func yearFiles(dir string) (map[int]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := historyExt(e.Name()); !ok {
			continue
		}
		y, ok := fileYear(e.Name())
		if !ok {
			continue
		}
		if prev, seen := out[y]; seen && strings.HasSuffix(prev, parquetExt) {
			continue
		}
		out[y] = filepath.Join(dir, e.Name())
	}
	return out, nil
}

// Load relit l'historique M1 complet d'un symbole sur [from, to].
//
// Robuste aux maladresses : fichiers parasites ignorés, années hors de la
// période non ouvertes, doublons d'horodatage dédupliqués, ordre rétabli.
func Load(historyDir, symbol string, from, to time.Time) (core.Series, error) {
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return nil, fmt.Errorf("période invalide : début (%s) postérieur à la fin (%s)",
			from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	dir := filepath.Join(historyDir, symbol)
	entries, err := yearFiles(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("aucun historique pour %s dans %s — lancer le téléchargement d'abord", symbol, historyDir)
		}
		return nil, err
	}
	type yearFile struct {
		year int
		path string
	}
	var files []yearFile
	for y, path := range entries {
		if !from.IsZero() && y < from.UTC().Year() {
			continue
		}
		if !to.IsZero() && y > to.UTC().Year() {
			continue
		}
		files = append(files, yearFile{year: y, path: path})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("aucun historique pour %s sur la période demandée", symbol)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].year < files[j].year })

	// Pré-dimensionnement depuis les en-têtes : quinze ans de M1 sur une
	// paire font cinq millions de bougies, soit un demi-gigaoctet. Laisser
	// la tranche grandir toute seule la recopie une dizaine de fois et fait
	// culminer la mémoire au double du nécessaire — sur un poste modeste,
	// c'est la différence entre « ça charge » et « ça n'entre pas ».
	//
	// C'est une ESTIMATION HAUTE (les bornes de dates peuvent en écarter
	// une partie) : elle ne sert qu'à réserver, jamais à annoncer un
	// nombre de bougies.
	estimate := int64(0)
	for _, f := range files {
		if h, err := ReadHeader(f.path); err == nil {
			estimate += h.Count
		}
	}
	all := make(core.Series, 0, estimate)
	for _, f := range files {
		part, _, err := ReadSeries(f.path, from, to)
		if err != nil {
			return nil, err
		}
		all = append(all, part...)
	}
	return Sanitize(all), nil
}

// Sanitize rétablit l'invariant des séries : tri par temps croissant et
// déduplication (on garde la DERNIÈRE bougie d'un horodatage, la plus
// récemment téléchargée).
func Sanitize(s core.Series) core.Series {
	if len(s) < 2 {
		return s
	}
	sorted := sort.SliceIsSorted(s, func(i, j int) bool { return s[i].Time.Before(s[j].Time) })
	if !sorted {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Time.Before(s[j].Time) })
	}
	out := s[:1]
	for _, bar := range s[1:] {
		if bar.Time.Equal(out[len(out)-1].Time) {
			out[len(out)-1] = bar
			continue
		}
		out = append(out, bar)
	}
	return out
}

// Inventory décrit ce qui est disponible localement pour un symbole.
type Inventory struct {
	Symbol string
	Years  []int
	Bars   int64
	First  time.Time
	Last   time.Time
	// Partial : années présentes mais INCOMPLÈTES (des jours manquent).
	// Elles sont retéléchargées à la prochaine demande, et l'interface les
	// distingue d'une année pleine plutôt que de les compter comme faites.
	Partial []int
	// Legacy : années encore stockées dans l'ancien format .gwb. Elles se
	// lisent, mais aucun autre outil ne sait les ouvrir : `gw migrate` les
	// convertit. Les afficher plutôt que les convertir en douce laisse la
	// décision — et le moment — à l'utilisateur.
	Legacy []int
}

// Catalog inventorie le dossier d'historique sans décoder les bougies.
func Catalog(historyDir string) ([]Inventory, error) {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Inventory
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inv := Inventory{Symbol: e.Name()}
		files, err := yearFiles(filepath.Join(historyDir, e.Name()))
		if err != nil {
			continue
		}
		for y, path := range files {
			h, err := ReadHeader(path)
			if err != nil {
				continue
			}
			inv.Years = append(inv.Years, y)
			inv.Bars += h.Count
			if !h.Complete() {
				inv.Partial = append(inv.Partial, y)
			}
			if isLegacy(path) {
				inv.Legacy = append(inv.Legacy, y)
			}
		}
		if len(inv.Years) == 0 {
			continue
		}
		sort.Ints(inv.Years)
		sort.Ints(inv.Partial)
		sort.Ints(inv.Legacy)
		inv.First = time.Date(inv.Years[0], time.January, 1, 0, 0, 0, 0, time.UTC)
		inv.Last = time.Date(inv.Years[len(inv.Years)-1], time.December, 31, 23, 59, 0, 0, time.UTC)
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}
