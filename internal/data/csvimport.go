package data

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// CSVOptions : ce que le fichier ne dit pas de lui-même.
type CSVOptions struct {
	// Location : fuseau des horodatages SANS décalage explicite. UTC par
	// défaut. Un export MetaTrader est souvent à l'heure du courtier
	// (UTC+2/+3) : le lire en UTC décalerait toutes les bougies, week-end
	// compris, sans qu'aucun chiffre n'ait l'air faux.
	Location *time.Location
	// TimeLayout : format Go de la date (« 2006.01.02 15:04 »). Vide =
	// détecté sur le fichier, et REFUSÉ s'il est ambigu (01/02 : 1er
	// février ou 2 janvier ?).
	TimeLayout string
	// Columns : noms des colonnes d'un fichier SANS entête, dans l'ordre
	// (« time,open,high,low,close,volume »). Jamais deviné : des colonnes
	// appariées par position au hasard donnent des prix crédibles et faux.
	Columns []string
}

// csvAliases : noms reconnus pour chaque colonne, en minuscules, sans
// espace ni ponctuation (« <DATE> », « Bid Open », « bid_open » se
// ramènent tous à une forme comparable par normalizeHeader).
var csvAliases = map[int][]string{
	colTime:     {"time", "timestamp", "datetime", "gmttime", "localtime", "ts"},
	colBidOpen:  {"bidopen", "open", "o"},
	colBidHigh:  {"bidhigh", "high", "h"},
	colBidLow:   {"bidlow", "low", "l"},
	colBidClose: {"bidclose", "close", "c"},
	colAskOpen:  {"askopen"},
	colAskHigh:  {"askhigh"},
	colAskLow:   {"asklow"},
	colAskClose: {"askclose"},
	colVolume:   {"volume", "vol", "v", "tickvol", "tickvolume"},
}

// csvDateCol : une DATE séparée de l'heure (exports MetaTrader :
// `<DATE>,<TIME>,<OPEN>…`). Elle est recollée à la colonne time.
const csvDateAlias = "date"

func normalizeHeader(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// csvLayouts : formats de date essayés, du plus au moins spécifique.
// JJ/MM et MM/JJ sont TOUS DEUX candidats : c'est le fichier entier qui
// tranche, et s'il ne tranche pas, on refuse.
var csvLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006.01.02 15:04:05",
	"2006.01.02 15:04",
	"20060102 150405",
	"20060102 15:04:05",
	"20060102 15:04",
	"01/02/2006 15:04:05.000",
	"01/02/2006 15:04:05",
	"01/02/2006 15:04",
	"02/01/2006 15:04:05.000",
	"02/01/2006 15:04:05",
	"02/01/2006 15:04",
	"02.01.2006 15:04:05.000",
	"02.01.2006 15:04:05",
	"02.01.2006 15:04",
}

// csvSample : lignes examinées pour choisir le format de date. Assez pour
// qu'un 13 du mois apparaisse dans un fichier de plus de quinze jours.
const csvSample = 50000

// ReadCSV lit un historique M1 au format texte (CSV, TSV, point-virgule ;
// gzip accepté).
//
// Règles, dans l'esprit du lecteur Parquet :
//   - colonnes appariées PAR LEUR NOM (alias), jamais par position sauf
//     si l'utilisateur les nomme (CSVOptions.Columns) ;
//   - `open/high/low/close` sans préfixe sont lus comme le côté BID (ce
//     que fait aussi l'import Parquet) ; l'ask n'est lu que s'il est
//     nommé, et un ask incomplet n'est pas un ask ;
//   - volume absent = NaN (« non mesuré »), jamais zéro ;
//   - une ligne illisible REFUSE le fichier, avec son numéro : un fichier
//     à moitié lu importé comme s'il était entier est pire qu'un refus.
func ReadCSV(path string, opts CSVOptions) (core.Series, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("gzip illisible : %w", err)
		}
		defer zr.Close()
		r = zr
	}
	return parseCSV(r, opts)
}

func parseCSV(r io.Reader, opts CSVOptions) (core.Series, error) {
	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	// Lignes non vides, numérotées pour les messages d'erreur.
	type row struct {
		n      int
		fields []string
	}
	var rows []row
	var sep rune
	lineNo := 0
	for sc.Scan() {
		lineNo++
		text := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if text == "" {
			continue
		}
		if sep == 0 {
			sep = detectSeparator(text)
		}
		fields := strings.Split(text, string(sep))
		for i := range fields {
			fields[i] = strings.Trim(strings.TrimSpace(fields[i]), `"`)
		}
		rows = append(rows, row{lineNo, fields})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("fichier vide")
	}

	// Entête : nommée par le fichier, ou par l'utilisateur.
	names := rows[0].fields
	data := rows[1:]
	if len(opts.Columns) > 0 {
		names, data = opts.Columns, rows
	}
	idx, dateCol, err := csvColumns(names)
	if err != nil {
		if len(opts.Columns) == 0 {
			return nil, fmt.Errorf("%w — fichier sans entête ? Nommer ses colonnes avec "+
				"--columns time,open,high,low,close,volume", err)
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("aucune ligne de données après l'entête")
	}

	stamp := func(f []string) string {
		s := f[idx[colTime]]
		if dateCol >= 0 {
			s = f[dateCol] + " " + s
		}
		return s
	}
	width := 0
	for c := range idx {
		width = max(width, idx[c]+1)
	}
	width = max(width, dateCol+1)

	// Nombres : le point décimal, ou la virgule décimale d'un fichier à
	// point-virgule (tableur francophone, et nos propres exports).
	decimalComma := sep == ';'
	num := func(s string) (float64, error) {
		if decimalComma {
			s = strings.Replace(s, ",", ".", 1)
		}
		return strconv.ParseFloat(s, 64)
	}

	// Horodatage : un nombre (époque Unix) ou une date dont le format est
	// choisi sur l'échantillon.
	layout := opts.TimeLayout
	epoch := false
	if layout == "" {
		sample := make([]string, 0, min(len(data), csvSample))
		for _, r := range data[:min(len(data), csvSample)] {
			if len(r.fields) < width {
				return nil, fmt.Errorf("ligne %d : %d champs, %d attendus", r.n, len(r.fields), width)
			}
			sample = append(sample, stamp(r.fields))
		}
		// Époque Unix : un entier d'au moins dix chiffres (2001 et après,
		// en secondes). Une date compacte « 20240102 » n'en est pas une.
		if v, err := strconv.ParseInt(sample[0], 10, 64); err == nil && dateCol < 0 && v >= 1e9 {
			epoch = true
		} else if layout, err = detectLayout(sample, loc); err != nil {
			return nil, err
		}
	}

	out := make(core.Series, 0, len(data))
	for _, r := range data {
		f := r.fields
		if len(f) < width {
			return nil, fmt.Errorf("ligne %d : %d champs, %d attendus", r.n, len(f), width)
		}
		var t time.Time
		if epoch {
			v, err := strconv.ParseInt(stamp(f), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ligne %d : horodatage %q illisible", r.n, stamp(f))
			}
			t = time.UnixMilli(toMillis(v, epochDivisor(v))).UTC()
		} else {
			parsed, err := time.ParseInLocation(layout, stamp(f), loc)
			if err != nil {
				return nil, fmt.Errorf("ligne %d : date %q illisible au format %q", r.n, stamp(f), layout)
			}
			t = parsed.UTC()
		}
		var p [colCount]float64
		present := [colCount]bool{}
		for c := colBidOpen; c < colCount; c++ {
			if idx[c] < 0 {
				continue
			}
			raw := f[idx[c]]
			if raw == "" && (c >= colAskOpen) {
				continue // ask ou volume vides : absents, pas nuls
			}
			v, err := num(raw)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("ligne %d : valeur %q illisible", r.n, raw)
			}
			if c < colVolume && v <= 0 {
				return nil, fmt.Errorf("ligne %d : prix %q non positif", r.n, raw)
			}
			p[c], present[c] = v, true
		}
		if p[colBidHigh] < p[colBidLow] {
			return nil, fmt.Errorf("ligne %d : plus haut %g sous le plus bas %g — colonnes inversées ?",
				r.n, p[colBidHigh], p[colBidLow])
		}
		bar := core.Bar{
			Time:    t,
			BidOpen: p[colBidOpen], BidHigh: p[colBidHigh], BidLow: p[colBidLow], BidClose: p[colBidClose],
			Volume: math.NaN(),
		}
		if present[colVolume] {
			bar.Volume = p[colVolume]
		}
		if present[colAskOpen] && present[colAskHigh] && present[colAskLow] && present[colAskClose] {
			bar.AskOpen, bar.AskHigh, bar.AskLow, bar.AskClose =
				p[colAskOpen], p[colAskHigh], p[colAskLow], p[colAskClose]
		}
		out = append(out, bar)
	}
	return out, nil
}

// epochDivisor : secondes, millisecondes, microsecondes ou nanosecondes,
// d'après l'ordre de grandeur (même règle que le lecteur Parquet).
func epochDivisor(v int64) int64 {
	switch {
	case v < 1e11:
		return -1000 // secondes : à multiplier
	case v < 1e14:
		return 1
	case v < 1e17:
		return 1000
	default:
		return 1000000
	}
}

// detectSeparator : le séparateur le plus fréquent de la première ligne.
//
// À égalité, le point-virgule gagne : une ligne `1,10;1,11;…` (tableur
// francophone, virgule décimale) compte autant de virgules que de
// points-virgules, et c'est le point-virgule qui sépare.
func detectSeparator(line string) rune {
	best, count := ',', 0
	for _, r := range []rune{';', '\t', '|', ','} {
		if n := strings.Count(line, string(r)); n > count {
			best, count = r, n
		}
	}
	return best
}

// csvColumns apparie les noms aux colonnes attendues.
func csvColumns(names []string) (idx [colCount]int, dateCol int, err error) {
	for i := range idx {
		idx[i] = -1
	}
	dateCol = -1
	byName := map[string]int{}
	for i, n := range names {
		if _, seen := byName[normalizeHeader(n)]; !seen {
			byName[normalizeHeader(n)] = i
		}
	}
	for c, aliases := range csvAliases {
		for _, a := range aliases {
			if i, ok := byName[a]; ok {
				idx[c] = i
				break
			}
		}
	}
	// Date et heure séparées : `date` + `time`. Une colonne `date` seule
	// porte l'horodatage entier.
	if i, ok := byName[csvDateAlias]; ok {
		if idx[colTime] >= 0 {
			dateCol = i
		} else {
			idx[colTime] = i
		}
	}
	var missing []string
	for _, c := range []struct {
		col  int
		name string
	}{{colTime, "time"}, {colBidOpen, "open"}, {colBidHigh, "high"}, {colBidLow, "low"}, {colBidClose, "close"}} {
		if idx[c.col] < 0 {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		return idx, dateCol, fmt.Errorf("colonne(s) %s introuvable(s) dans l'entête %q",
			strings.Join(missing, ", "), strings.Join(names, ","))
	}
	return idx, dateCol, nil
}

// detectLayout choisit LE format qui lit tout l'échantillon.
//
// Plusieurs candidats (01/02/2024 : JJ/MM ou MM/JJ ?) = refus, pas pari :
// un mois et un jour intervertis donnent des bougies plausibles et fausses.
func detectLayout(sample []string, loc *time.Location) (string, error) {
	var ok []string
	for _, layout := range csvLayouts {
		good := true
		for _, s := range sample {
			if _, err := time.ParseInLocation(layout, s, loc); err != nil {
				good = false
				break
			}
		}
		if good {
			ok = append(ok, layout)
		}
	}
	switch {
	case len(ok) == 0:
		return "", fmt.Errorf("format de date non reconnu (%q) — le préciser avec --time-format "+
			"(format Go, ex. \"2006.01.02 15:04\")", sample[0])
	case len(ok) > 1 && !sameMeaning(ok, sample, loc):
		return "", fmt.Errorf("format de date AMBIGU (%q se lit %s) — le préciser avec --time-format",
			sample[0], strings.Join(ok, " ou "))
	}
	return ok[0], nil
}

// sameMeaning : plusieurs formats peuvent lire le même échantillon sans
// ambiguïté réelle (avec ou sans millisecondes). Ils ne sont équivalents
// que s'ils donnent le même instant pour chaque ligne.
func sameMeaning(layouts, sample []string, loc *time.Location) bool {
	for _, s := range sample {
		first, _ := time.ParseInLocation(layouts[0], s, loc)
		for _, l := range layouts[1:] {
			t, _ := time.ParseInLocation(l, s, loc)
			if !t.Equal(first) {
				return false
			}
		}
	}
	return true
}

// medianStep : écart médian entre bougies consécutives d'une même
// journée. Sert à refuser un historique qui n'est pas du M1 : importé
// comme tel, un fichier H1 ferait des « bougies M1 » d'une heure.
func medianStep(s core.Series) (time.Duration, bool) {
	var steps []time.Duration
	for i := 1; i < len(s) && len(steps) < 100000; i++ {
		d := s[i].Time.Sub(s[i-1].Time)
		if d > 0 && d < 6*time.Hour {
			steps = append(steps, d)
		}
	}
	if len(steps) == 0 {
		return 0, false
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
	return steps[len(steps)/2], true
}
