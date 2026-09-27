package data

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// DefaultSource : source d'historique d'une installation neuve.
const DefaultSource = "dukascopy"

// DownloadProgress décrit l'avancement d'un téléchargement, publié vers la
// TUI. Les chiffres sont RÉELS : rien n'est extrapolé pour faire joli.
type DownloadProgress struct {
	Symbol string
	Year   int
	Source string
	// UnitsDone / UnitsTotal : unités traitées, dans l'unité de la source
	// (Unit : jours, semaines).
	UnitsDone, UnitsTotal int
	Unit                  string
	Bars                  int
	Skipped               int // unités sans donnée publiée : normal (week-ends, fériés)
	Failures              int
	CurrentStep           string
	Done                  bool
	Err                   error
}

// Downloader télécharge l'historique M1 depuis UNE source, et l'écrit.
//
// Il ne sait rien du fournisseur : il demande une année à la source,
// écrit le fichier avec le compte des manques et le nom de la source, et
// tient le journal. C'est ce qui permet de changer de fournisseur par la
// configuration (`history.source`) sans toucher au reste.
type Downloader struct {
	Source     Source
	HistoryDir string
	Logger     *slog.Logger
	// Now : horloge (les tests la figent).
	Now func() time.Time
}

// NewDownloader construit un téléchargeur sur la source nommée.
func NewDownloader(source, historyDir string, concurrency int, logger *slog.Logger) (*Downloader, error) {
	src, err := NewSource(source, SourceOptions{Concurrency: concurrency})
	if err != nil {
		return nil, err
	}
	return &Downloader{Source: src, HistoryDir: historyDir, Logger: logger, Now: time.Now}, nil
}

// DownloadYear télécharge une année complète pour un symbole et l'écrit.
//
// Un symbole que la source ne publie pas renvoie ErrNotServed (à sauter,
// pas à traiter comme une panne) ; une année antérieure à la source
// renvoie 0 bougie sans erreur.
func (d *Downloader) DownloadYear(ctx context.Context, symbol string, year int,
	progress func(DownloadProgress)) (int, error) {

	inst, err := LookupInstrument(symbol)
	if err != nil {
		return 0, err
	}
	info := d.Source.Info()
	if !d.Source.Serves(symbol) {
		return 0, fmt.Errorf("%s : %w (%s)", symbol, ErrNotServed, info.Label)
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	if year < info.FirstYear || year > now().UTC().Year() {
		return 0, nil
	}

	fetched, err := d.Source.FetchYear(ctx, inst, symbol, year, now().UTC(), func(s Step) {
		if progress != nil {
			progress(DownloadProgress{
				Symbol: symbol, Year: year, Source: info.Name,
				UnitsDone: s.Done, UnitsTotal: s.Total, Unit: info.Unit,
				Bars: s.Bars, Skipped: s.Skipped, Failures: s.Failures,
				CurrentStep: s.Current,
			})
		}
	})
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	all := fetched.Bars
	if len(all) == 0 {
		if fetched.FirstErr != nil {
			return 0, fetched.FirstErr
		}
		return 0, nil // année sans donnée (instrument plus récent)
	}
	all = Sanitize(all)

	path := FilePath(d.HistoryDir, symbol, year)
	header := FileHeader{
		Symbol: symbol, Year: year, Scale: inst.Scale(),
		Failures: int32(fetched.Missing), Source: info.Name,
	}
	if err := WriteSeries(path, header, all); err != nil {
		return 0, err
	}
	if fetched.Missing > 0 && d.Logger != nil {
		// Une année partiellement téléchargée est ÉCRITE, mais son en-tête
		// porte le nombre d'unités manquantes : la prochaine demande la
		// refera au lieu de la sauter, et l'interface l'affiche comme
		// incomplète plutôt que comme faite.
		d.Logger.Warn("année incomplète",
			"symbole", symbol, "annee", year, "source", info.Name,
			"manquant", fetched.Missing, "unite", info.Unit, "premiere_erreur", fetched.FirstErr)
	}
	return len(all), nil
}

// NeedsDownload dit si une année doit être (re)téléchargée.
//
// Trois cas la rendent nécessaire : le fichier est absent, il est
// illisible, ou il est INCOMPLET. Le troisième est le piège : sans lui,
// une année écrite avec des trous passait pour faite et ses jours
// manquants le restaient définitivement.
//
// L'année EN COURS est toujours à refaire — elle est incomplète par
// nature, puisque le marché n'a pas fini de la produire.
func NeedsDownload(historyDir, symbol string, year, currentYear int) bool {
	if year >= currentYear {
		return true
	}
	// L'année peut n'exister que dans l'ANCIEN format : chercher
	// uniquement le Parquet ferait retélécharger, année par année, un
	// historique déjà présent sur le disque.
	h, err := ReadHeader(FilePath(historyDir, symbol, year))
	if err != nil {
		h, err = ReadHeader(legacyFilePath(historyDir, symbol, year))
	}
	if err != nil {
		return true
	}
	return !h.Complete()
}

// MissingYears renvoie les années à (re)télécharger pour un symbole.
func (d *Downloader) MissingYears(symbol string, from, to int) []int {
	current := time.Now().UTC().Year()
	var out []int
	for y := from; y <= to; y++ {
		if NeedsDownload(d.HistoryDir, symbol, y, current) {
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}

// IsWeekend : samedi ou dimanche en UTC.
//
// L'historique ne contient JAMAIS ces jours-là, quelle qu'en soit la
// source : Dukascopy ne les demande pas, FXCM et les imports les
// écartent. Ce n'est pas une coquetterie. `core.LastBarsOfWeek` (clôture
// de fin de semaine du backtest, étiquetage de colibri_v1_2) découpe par
// semaine ISO, du lundi au dimanche : une bougie du dimanche soir — la
// réouverture du forex — serait la dernière de SA semaine, et la clôture
// de fin de semaine aurait lieu après le week-end qu'elle doit éviter.
func IsWeekend(t time.Time) bool {
	wd := t.UTC().Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// DropWeekend retire les bougies du samedi et du dimanche (cf. IsWeekend)
// et dit combien.
func DropWeekend(s core.Series) (core.Series, int) {
	out := s[:0]
	for _, b := range s {
		if !IsWeekend(b.Time) {
			out = append(out, b)
		}
	}
	return out, len(s) - len(out)
}
