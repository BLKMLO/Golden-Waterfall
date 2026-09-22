package data

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Converted décrit un fichier converti de .gwb vers Parquet.
type Converted struct {
	Symbol string
	Year   int
	Bars   int64
	Before int64 // taille du .gwb, en octets
	After  int64 // taille du .parquet, en octets
	// Removed : l'original a été supprimé après vérification.
	Removed bool
	Err     error
}

// MigrationReport résume une migration.
type MigrationReport struct {
	Files     []Converted
	Converted int
	Failed    int
	Before    int64
	After     int64
	Freed     int64
}

// LegacyFiles liste les .gwb restant dans un dossier d'historique.
func LegacyFiles(historyDir string) ([]string, error) {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(historyDir, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && isLegacy(f.Name()) {
				if _, ok := fileYear(f.Name()); ok {
					out = append(out, filepath.Join(dir, f.Name()))
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Migrate convertit en Parquet tous les .gwb d'un dossier d'historique.
//
// L'original n'est supprimé (option `remove`) qu'APRÈS relecture du
// fichier écrit et comparaison bougie à bougie des extrémités. Une
// conversion non vérifiée qui efface la source est la seule façon de
// perdre pour de bon un historique qui a coûté des heures de
// téléchargement.
func Migrate(historyDir string, remove bool, progress func(Converted)) (MigrationReport, error) {
	files, err := LegacyFiles(historyDir)
	if err != nil {
		return MigrationReport{}, err
	}
	var report MigrationReport
	for _, path := range files {
		c := convertOne(path, remove)
		report.Files = append(report.Files, c)
		if c.Err != nil {
			report.Failed++
		} else {
			report.Converted++
			report.Before += c.Before
			report.After += c.After
			if c.Removed {
				report.Freed += c.Before
			}
		}
		if progress != nil {
			progress(c)
		}
	}
	return report, nil
}

func convertOne(path string, remove bool) Converted {
	symbol, year := symbolYearFromPath(path)
	c := Converted{Symbol: symbol, Year: year}
	if info, err := os.Stat(path); err == nil {
		c.Before = info.Size()
	}

	series, header, err := readLegacySeries(path, time.Time{}, time.Time{})
	if err != nil {
		c.Err = err
		return c
	}
	c.Bars = int64(len(series))
	if header.Symbol != "" {
		c.Symbol = header.Symbol
	}
	if header.Year != 0 {
		c.Year = header.Year
	}

	target := path[:len(path)-len(legacyExt)] + parquetExt
	if err := WriteSeries(target, header, series); err != nil {
		c.Err = err
		return c
	}
	if info, err := os.Stat(target); err == nil {
		c.After = info.Size()
	}

	// Relecture de contrôle : c'est elle qui autorise la suppression.
	back, h, err := ReadSeries(target, time.Time{}, time.Time{})
	if err != nil {
		c.Err = fmt.Errorf("fichier écrit mais illisible : %w", err)
		return c
	}
	if err := sameSeries(series, back); err != nil {
		c.Err = err
		return c
	}
	if h.Failures != header.Failures {
		c.Err = fmt.Errorf("jours en échec perdus à la conversion : %d au lieu de %d",
			h.Failures, header.Failures)
		return c
	}
	if remove {
		if err := os.Remove(path); err != nil {
			c.Err = fmt.Errorf("converti, mais l'original n'a pas pu être supprimé : %w", err)
			return c
		}
		c.Removed = true
	}
	return c
}

// sameSeries compare deux séries bougie à bougie.
//
// Pas seulement le compte et les extrémités : une inversion au milieu
// passerait, et c'est précisément ce qu'une conversion de format peut
// produire. Le coût est négligeable devant l'écriture.
func sameSeries(want, got core.Series) error {
	if len(want) != len(got) {
		return fmt.Errorf("%d bougies relues pour %d écrites", len(got), len(want))
	}
	for i := range want {
		a, b := want[i], got[i]
		if !a.Time.Equal(b.Time) {
			return fmt.Errorf("bougie %d : horodatage %s au lieu de %s", i, b.Time, a.Time)
		}
		if a.HasAsk() != b.HasAsk() {
			return fmt.Errorf("bougie %d : présence du côté ask non conservée", i)
		}
		for j, pair := range [][2]float64{
			{a.BidOpen, b.BidOpen}, {a.BidHigh, b.BidHigh},
			{a.BidLow, b.BidLow}, {a.BidClose, b.BidClose},
			{a.AskOpen, b.AskOpen}, {a.AskHigh, b.AskHigh},
			{a.AskLow, b.AskLow}, {a.AskClose, b.AskClose},
			{a.Volume, b.Volume},
		} {
			if pair[0] != pair[1] {
				return fmt.Errorf("bougie %d champ %d : %v au lieu de %v", i, j, pair[1], pair[0])
			}
		}
	}
	return nil
}

// ImportReport résume un import de fichiers extérieurs.
type ImportReport struct {
	Symbol string
	Years  []int
	Bars   int64
	Files  []string
}

// Import verse un ou plusieurs fichiers Parquet extérieurs dans
// l'historique local, découpés par année.
//
// Ce qui est écrit est marqué IMPORTÉ : personne n'a compté les jours
// manquants de ces fichiers, donc `Complete()` répond non et l'écran
// Données le distingue d'une année téléchargée. Supposer complet un
// fichier dont on ne sait rien serait la pire des politesses.
func Import(historyDir, symbol string, paths []string) (ImportReport, error) {
	inst, err := LookupInstrument(symbol)
	if err != nil {
		return ImportReport{}, err
	}
	var all core.Series
	for _, p := range paths {
		s, _, err := ReadSeries(p, time.Time{}, time.Time{})
		if err != nil {
			return ImportReport{}, fmt.Errorf("%s : %w", p, err)
		}
		if len(s) == 0 {
			return ImportReport{}, fmt.Errorf("%s : aucune bougie lisible", p)
		}
		all = append(all, s...)
	}
	all = Sanitize(all)
	if len(all) == 0 {
		return ImportReport{}, fmt.Errorf("aucune bougie à importer")
	}

	byYear := map[int]core.Series{}
	for _, bar := range all {
		y := bar.Time.UTC().Year()
		byYear[y] = append(byYear[y], bar)
	}
	report := ImportReport{Symbol: symbol, Bars: int64(len(all))}
	for y := range byYear {
		report.Years = append(report.Years, y)
	}
	sort.Ints(report.Years)

	for _, y := range report.Years {
		path := FilePath(historyDir, symbol, y)
		header := FileHeader{
			Symbol: symbol, Year: y, Scale: inst.Scale(), Imported: true,
		}
		if err := WriteSeries(path, header, byYear[y]); err != nil {
			return report, err
		}
		report.Files = append(report.Files, path)
	}
	return report, nil
}
