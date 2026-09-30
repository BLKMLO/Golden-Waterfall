package data

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	// Skipped : un Parquet de la même année existait déjà ; rien n'a été
	// écrit ni supprimé.
	Skipped bool
	// Removed : l'original a été supprimé après vérification.
	Removed bool
	Err     error
}

// MigrationReport résume une migration.
type MigrationReport struct {
	Files     []Converted
	Converted int
	Failed    int
	// Skipped : .gwb laissés intacts parce qu'un Parquet de la même année
	// existe déjà.
	Skipped int
	Before  int64
	After   int64
	Freed   int64
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
		if c.Skipped {
			report.Skipped++
		} else if c.Err != nil {
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
	if _, err := os.Stat(target); err == nil {
		// Un Parquet existe déjà pour cette année. Soit c'est la
		// conversion de CE fichier (premier passage sans --remove) : on la
		// garde et on passe à la vérification. Soit l'année a été
		// retéléchargée ou importée depuis : c'est ce Parquet que
		// l'historique lit, et le remplacer par l'ancien fichier effaçait
		// des données plus récentes. Le .gwb reste alors intact.
		existing, _, err := ReadSeries(target, time.Time{}, time.Time{})
		if err != nil || sameSeries(series, existing) != nil {
			c.Skipped = true
			c.Err = fmt.Errorf("%s existe déjà avec un autre contenu : conversion sautée, .gwb laissé intact",
				filepath.Base(target))
			return c
		}
	} else if err := WriteSeries(target, header, series); err != nil {
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
			if pair[0] != pair[1] && !(math.IsNaN(pair[0]) && math.IsNaN(pair[1])) {
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
	// WeekendDropped : bougies du samedi et du dimanche écartées (cf.
	// IsWeekend). Dites, jamais passées sous silence.
	WeekendDropped int
	// Duplicates : horodatages présents plusieurs fois (la dernière
	// occurrence est gardée).
	Duplicates int
}

// ImportOptions : réglages d'un import.
type ImportOptions struct {
	// CSV : ce que les fichiers texte ne disent pas d'eux-mêmes.
	CSV CSVOptions
	// Replace : autorise l'écrasement d'années déjà présentes. Sans lui,
	// l'import REFUSE : un fichier tiers de trois mois écrasait en silence
	// une année téléchargée et complète.
	Replace bool
}

// Import verse des fichiers extérieurs (Parquet, CSV/TSV, éventuellement
// gzip) dans l'historique local, découpés par année.
//
// Ce qui est écrit est marqué IMPORTÉ : personne n'a compté les jours
// manquants de ces fichiers, donc `Complete()` répond non et l'écran
// Données le distingue d'une année téléchargée. Supposer complet un
// fichier dont on ne sait rien serait la pire des politesses.
//
// Trois refus protègent l'historique : un fichier qui n'est pas du M1,
// une année déjà présente (sauf Replace), un fichier illisible. Rien
// n'est écrit si l'un d'eux tombe.
func Import(historyDir, symbol string, paths []string, opts ImportOptions) (ImportReport, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	inst, err := LookupInstrument(symbol)
	if err != nil {
		return ImportReport{}, err
	}
	var all core.Series
	for _, p := range paths {
		var s core.Series
		if isTextHistory(p) {
			s, err = ReadCSV(p, opts.CSV)
		} else {
			s, _, err = ReadSeries(p, time.Time{}, time.Time{})
		}
		if err != nil {
			return ImportReport{}, fmt.Errorf("%s : %w", p, err)
		}
		if len(s) == 0 {
			return ImportReport{}, fmt.Errorf("%s : aucune bougie lisible", p)
		}
		// Le CSV est contrôlé ligne à ligne à la lecture ; un Parquet tiers
		// l'est ici, avec la même règle.
		for _, bar := range s {
			if err := checkBar(bar); err != nil {
				return ImportReport{}, fmt.Errorf("%s : bougie du %s : %w", p,
					bar.Time.UTC().Format("2006-01-02 15:04"), err)
			}
		}
		all = append(all, s...)
	}
	report := ImportReport{Symbol: symbol}
	n := len(all)
	all = Sanitize(all)
	report.Duplicates = n - len(all)
	all, report.WeekendDropped = DropWeekend(all)
	if len(all) == 0 {
		return report, fmt.Errorf("aucune bougie à importer (%d écartées : samedi ou dimanche)",
			report.WeekendDropped)
	}
	if step, ok := medianStep(all); ok && step != time.Minute {
		return report, fmt.Errorf("l'historique doit être en M1 : écart médian entre bougies de %s. "+
			"Les autres unités de temps sont calculées à partir du M1", step)
	}
	report.Bars = int64(len(all))

	byYear := map[int]core.Series{}
	for _, bar := range all {
		y := bar.Time.UTC().Year()
		byYear[y] = append(byYear[y], bar)
	}
	for y := range byYear {
		report.Years = append(report.Years, y)
	}
	sort.Ints(report.Years)

	if !opts.Replace {
		var taken []string
		for _, y := range report.Years {
			if _, err := os.Stat(FilePath(historyDir, symbol, y)); err == nil {
				taken = append(taken, strconv.Itoa(y))
			} else if _, err := os.Stat(legacyFilePath(historyDir, symbol, y)); err == nil {
				taken = append(taken, strconv.Itoa(y))
			}
		}
		if len(taken) > 0 {
			return report, fmt.Errorf("%s : année(s) %s déjà présente(s) dans l'historique — "+
				"rien n'a été écrit. --replace pour les remplacer par l'import",
				symbol, strings.Join(taken, ", "))
		}
	}

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

// isTextHistory : l'extension dit s'il s'agit d'un fichier texte.
func isTextHistory(path string) bool {
	p := strings.ToLower(strings.TrimSuffix(strings.ToLower(path), ".gz"))
	for _, ext := range []string{".csv", ".txt", ".tsv"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}
