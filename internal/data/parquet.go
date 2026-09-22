package data

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/parquet-go/parquet-go/format"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Extension des fichiers d'historique.
const parquetExt = ".parquet"

// Clés de métadonnées. Elles portent ce que le schéma ne dit pas, à
// commencer par le nombre de jours manquants — l'invariant d'honnêteté du
// stockage. Un lecteur tiers les voit comme des paires clé/valeur
// ordinaires ; il n'a pas besoin de les comprendre pour lire les prix.
const (
	metaSymbol    = "gw.symbol"
	metaYear      = "gw.year"
	metaFailures  = "gw.failures"
	metaTimeframe = "gw.timeframe"
	metaSource    = "gw.source"
	metaWriter    = "gw.writer"
)

// rowGroupSize : un groupe de lignes par mois environ (une année de M1
// vaut ≈ 372 000 bougies). C'est ce qui permet de lire trois mois sans
// décompresser l'année : les groupes dont la plage de dates ne croise pas
// la période demandée ne sont jamais ouverts.
const rowGroupSize = 32 * 1024

// parquetBar est la ligne du fichier.
//
// Les colonnes portent des noms explicites et des types standard : un
// horodatage TIMESTAMP(MILLIS, UTC) et des DOUBLE. N'importe quel outil
// (pandas, polars, DuckDB, Spark) ouvre ce fichier sans rien savoir de
// Golden Waterfall — c'est tout l'intérêt d'avoir abandonné un format
// maison.
//
// Le côté ask est OPTIONNEL, donc NULL quand il manque. Zéro aurait été
// un prix, et un outil tiers l'aurait additionné : c'est la règle
// « — plutôt que zéro » de l'interface, appliquée au fichier.
type parquetBar struct {
	Time     int64    `parquet:"time,timestamp(millisecond)"`
	BidOpen  float64  `parquet:"bid_open"`
	BidHigh  float64  `parquet:"bid_high"`
	BidLow   float64  `parquet:"bid_low"`
	BidClose float64  `parquet:"bid_close"`
	AskOpen  *float64 `parquet:"ask_open,optional"`
	AskHigh  *float64 `parquet:"ask_high,optional"`
	AskLow   *float64 `parquet:"ask_low,optional"`
	AskClose *float64 `parquet:"ask_close,optional"`
	Volume   float64  `parquet:"volume"`
}

// Index des colonnes dans le schéma, dans l'ordre de parquetBar. Les
// nommer évite les 0, 1, 2… disséminés dans le code de lecture.
const (
	colTime = iota
	colBidOpen
	colBidHigh
	colBidLow
	colBidClose
	colAskOpen
	colAskHigh
	colAskLow
	colAskClose
	colVolume
	colCount
)

// writeBatch : nombre de bougies converties d'un coup. Le tampon de
// pointeurs du côté ask est réutilisé d'un lot à l'autre, sinon écrire
// une année coûterait un million et demi d'allocations.
const writeBatch = 8192

// WriteSeries écrit une série M1 d'UNE année dans un fichier Parquet.
//
// L'écriture passe par un fichier temporaire renommé à la fin : une
// interruption (Ctrl+C, coupure) laisse l'ancien fichier intact plutôt
// qu'un fichier tronqué que la relecture prendrait pour des données.
func WriteSeries(path string, header FileHeader, series core.Series) error {
	if header.Scale <= 0 {
		return fmt.Errorf("échelle de prix invalide pour %s : %d", header.Symbol, header.Scale)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(tmp) // no-op si le rename a réussi
	}()

	header.Count = int64(len(series))
	options := []parquet.WriterOption{
		// zstd au niveau par défaut. Mesuré sur une année de M1 :
		// 9,2 Mo contre 14,9 pour l'ancien format à taille fixe, écriture
		// 485 ms, lecture 139 ms. Le niveau « rapide » écrirait en 370 ms
		// pour 9,7 Mo — mais l'écriture se produit derrière un
		// téléchargement réseau qui dure des heures, alors que la taille,
		// elle, reste sur le disque pour toujours.
		parquet.Compression(&zstd.Codec{Level: zstd.DefaultLevel}),
		parquet.MaxRowsPerRowGroup(rowGroupSize),
		parquet.KeyValueMetadata(metaSymbol, header.Symbol),
		parquet.KeyValueMetadata(metaYear, strconv.Itoa(header.Year)),
		parquet.KeyValueMetadata(metaTimeframe, "M1"),
		parquet.KeyValueMetadata(metaWriter, "golden-waterfall"),
	}
	if header.Imported {
		// Aucun compte de jours manquants : ce fichier vient d'ailleurs et
		// personne ne les a comptés. Écrire « 0 » en ferait une année
		// déclarée complète sur la foi de rien.
		options = append(options, parquet.KeyValueMetadata(metaSource, "import"))
	} else {
		options = append(options,
			parquet.KeyValueMetadata(metaFailures, strconv.Itoa(int(header.Failures))),
			parquet.KeyValueMetadata(metaSource, "dukascopy"))
	}
	w := parquet.NewGenericWriter[parquetBar](f, options...)

	scale := float64(header.Scale)
	rows := make([]parquetBar, 0, writeBatch)
	asks := make([]float64, 0, 4*writeBatch)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if _, err := w.Write(rows); err != nil {
			return err
		}
		rows, asks = rows[:0], asks[:0]
		return nil
	}
	round := rounder(series, scale)

	for _, bar := range series {
		row := parquetBar{
			Time:     bar.Time.UTC().UnixMilli(),
			BidOpen:  round(bar.BidOpen),
			BidHigh:  round(bar.BidHigh),
			BidLow:   round(bar.BidLow),
			BidClose: round(bar.BidClose),
			Volume:   bar.Volume,
		}
		if bar.HasAsk() {
			n := len(asks)
			asks = append(asks, round(bar.AskOpen), round(bar.AskHigh),
				round(bar.AskLow), round(bar.AskClose))
			row.AskOpen, row.AskHigh = &asks[n], &asks[n+1]
			row.AskLow, row.AskClose = &asks[n+2], &asks[n+3]
		}
		rows = append(rows, row)
		if len(rows) == writeBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// rounder choisit l'arrondi appliqué aux prix avant écriture.
//
// Pourquoi arrondir : Dukascopy publie des ENTIERS mis à l'échelle. En
// les divisant, on obtient des doubles dont les derniers bits de mantisse
// sont du bruit de représentation — des chiffres qui n'existent pas dans
// la cotation. Les écrire tels quels fait tripler la taille du fichier :
// un compresseur ne sait rien faire d'un bruit de bas de mantisse.
//
// Pourquoi VÉRIFIER l'arrondi : un fichier importé peut venir d'une
// source plus fine que ce que `Instruments` déclare. Arrondir au jugé y
// perdrait des cotations réelles. On monte donc l'échelle par décades
// jusqu'à ce que l'aller-retour soit exact ; si aucune ne l'est, on
// n'arrondit pas du tout et le fichier grossit — un fichier trois fois
// plus gros vaut mieux qu'un prix inventé.
func rounder(series core.Series, scale float64) func(float64) float64 {
	const maxScale = 1e9
	for s := scale; s <= maxScale; s *= 10 {
		exact := true
		for i := range series {
			for _, v := range []float64{
				series[i].BidOpen, series[i].BidHigh, series[i].BidLow, series[i].BidClose,
				series[i].AskOpen, series[i].AskHigh, series[i].AskLow, series[i].AskClose,
			} {
				if v != 0 && math.Round(v*s)/s != v {
					exact = false
					break
				}
			}
			if !exact {
				break
			}
		}
		if exact {
			return func(v float64) float64 { return math.Round(v*s) / s }
		}
	}
	return func(v float64) float64 { return v }
}

// openParquet ouvre le fichier et lit son pied de page (schéma,
// métadonnées, statistiques par groupe de lignes). Aucune bougie n'est
// décodée à ce stade.
func openParquet(path string) (*os.File, *parquet.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%s : fichier Parquet illisible (%w)", path, err)
	}
	return f, pf, nil
}

// headerFrom reconstitue l'en-tête depuis les métadonnées du fichier.
//
// Un fichier écrit par un AUTRE outil n'a pas ces clés : le symbole et
// l'année viennent alors du nom de fichier, et le nombre de jours en
// échec est INCONNU. Inconnu se dit zéro ici — mais Complete() n'est
// alors qu'une supposition, ce que `Imported` signale.
func headerFrom(path string, pf *parquet.File) FileHeader {
	h := FileHeader{Count: pf.NumRows()}
	lookup := func(key string) (string, bool) {
		for _, kv := range pf.Metadata().KeyValueMetadata {
			if kv.Key == key {
				return kv.Value, true
			}
		}
		return "", false
	}
	if v, ok := lookup(metaSymbol); ok {
		h.Symbol = v
	}
	if v, ok := lookup(metaYear); ok {
		if y, err := strconv.Atoi(v); err == nil {
			h.Year = y
		}
	}
	if v, ok := lookup(metaFailures); ok {
		if n, err := strconv.Atoi(v); err == nil {
			h.Failures = int32(n)
		}
	} else {
		h.Imported = true
	}
	if _, ok := lookup(metaWriter); !ok {
		h.Imported = true
	}
	if h.Symbol == "" || h.Year == 0 {
		sym, year := symbolYearFromPath(path)
		if h.Symbol == "" {
			h.Symbol = sym
		}
		if h.Year == 0 {
			h.Year = year
		}
	}
	if inst, err := LookupInstrument(h.Symbol); err == nil {
		h.Scale = inst.Scale()
	}
	return h
}

// ReadHeader lit l'en-tête sans décoder les bougies (inventaire rapide).
func ReadHeader(path string) (FileHeader, error) {
	if isLegacy(path) {
		return readLegacyHeader(path)
	}
	f, pf, err := openParquet(path)
	if err != nil {
		return FileHeader{}, err
	}
	defer f.Close()
	return headerFrom(path, pf), nil
}

// ReadSeries relit un fichier d'historique, éventuellement borné à
// [from, to].
//
// Les groupes de lignes dont la plage de dates ne croise pas la période
// demandée ne sont jamais décompressés : lire trois mois ne coûte pas la
// lecture de l'année.
func ReadSeries(path string, from, to time.Time) (core.Series, FileHeader, error) {
	if isLegacy(path) {
		return readLegacySeries(path, from, to)
	}
	f, pf, err := openParquet(path)
	if err != nil {
		return nil, FileHeader{}, err
	}
	defer f.Close()
	h := headerFrom(path, pf)

	cols, err := columnIndexes(pf)
	if err != nil {
		return nil, h, fmt.Errorf("%s : %w", path, err)
	}

	fromMs, toMs := int64(math.MinInt64), int64(math.MaxInt64)
	if !from.IsZero() {
		fromMs = from.UTC().UnixMilli()
	}
	if !to.IsZero() {
		toMs = to.UTC().UnixMilli()
	}

	timeField := pf.Schema().Fields()[cols[colTime]]
	out := make(core.Series, 0, pf.NumRows())
	var times []int64
	unit := int64(0)
	var values [colCount][]float64
	var present [colCount][]bool

	for _, rg := range pf.RowGroups() {
		chunks := rg.ColumnChunks()
		times = times[:0]
		if err := readInt64Column(chunks[cols[colTime]], &times); err != nil {
			return nil, h, fmt.Errorf("%s : colonne time illisible (%w)", path, err)
		}
		if len(times) == 0 {
			continue
		}
		if unit == 0 {
			if unit, err = timeUnit(timeField, times[0]); err != nil {
				return nil, h, fmt.Errorf("%s : %w", path, err)
			}
		}
		for i := range times {
			times[i] = toMillis(times[i], unit)
		}
		// Le groupe est trié par temps : ses bornes suffisent à décider
		// s'il faut le décompresser.
		if times[len(times)-1] < fromMs || times[0] > toMs {
			continue
		}
		for c := colBidOpen; c < colCount; c++ {
			values[c] = values[c][:0]
			present[c] = present[c][:0]
			if cols[c] == -1 {
				// Colonne absente du fichier (un export tiers n'a pas de
				// côté ask) : toutes les valeurs sont ABSENTES, ce qui
				// n'est pas la même chose que nulles.
				values[c] = append(values[c], make([]float64, len(times))...)
				present[c] = append(present[c], make([]bool, len(times))...)
				continue
			}
			if err := readDoubleColumn(chunks[cols[c]], &values[c], &present[c]); err != nil {
				return nil, h, fmt.Errorf("%s : colonne %d illisible (%w)", path, c, err)
			}
			if len(values[c]) != len(times) {
				return nil, h, fmt.Errorf(
					"%s : %d valeurs dans une colonne pour %d horodatages — fichier incohérent",
					path, len(values[c]), len(times))
			}
		}
		for i, ms := range times {
			if ms < fromMs || ms > toMs {
				continue
			}
			// Bougie construite EN PLACE : une core.Bar fait une centaine
			// d'octets, et l'assembler puis la copier dans la tranche
			// coûtait un septième du temps de lecture.
			out = append(out, core.Bar{})
			bar := &out[len(out)-1]
			bar.Time = time.UnixMilli(ms).UTC()
			bar.BidOpen = values[colBidOpen][i]
			bar.BidHigh = values[colBidHigh][i]
			bar.BidLow = values[colBidLow][i]
			bar.BidClose = values[colBidClose][i]
			bar.Volume = values[colVolume][i]
			// Une seule valeur absente suffit à considérer que la bougie
			// n'a pas de côté ask : un demi-ask ne mesure aucun spread.
			if present[colAskOpen][i] && present[colAskHigh][i] &&
				present[colAskLow][i] && present[colAskClose][i] {
				bar.AskOpen = values[colAskOpen][i]
				bar.AskHigh = values[colAskHigh][i]
				bar.AskLow = values[colAskLow][i]
				bar.AskClose = values[colAskClose][i]
			}
		}
	}
	return out, h, nil
}

// columnIndexes fait correspondre les colonnes attendues à celles du
// fichier, PAR LEUR NOM.
//
// Se fier à l'ordre marcherait pour nos propres fichiers et casserait en
// silence sur un fichier importé : c'est exactement le défaut « cellule
// sous la mauvaise entête » que le reste du programme s'interdit.
func columnIndexes(pf *parquet.File) ([colCount]int, error) {
	var idx [colCount]int
	for i := range idx {
		idx[i] = -1
	}
	// Alias acceptés : un fichier venu d'ailleurs nomme rarement ses
	// colonnes comme nous. Le premier nom trouvé gagne.
	aliases := [colCount][]string{
		colTime:     {"time", "timestamp", "datetime", "date", "ts"},
		colBidOpen:  {"bid_open", "bidopen", "open", "o"},
		colBidHigh:  {"bid_high", "bidhigh", "high", "h"},
		colBidLow:   {"bid_low", "bidlow", "low", "l"},
		colBidClose: {"bid_close", "bidclose", "close", "c"},
		colAskOpen:  {"ask_open", "askopen"},
		colAskHigh:  {"ask_high", "askhigh"},
		colAskLow:   {"ask_low", "asklow"},
		colAskClose: {"ask_close", "askclose"},
		colVolume:   {"volume", "vol", "v"},
	}
	byName := map[string]int{}
	for i, field := range pf.Schema().Fields() {
		byName[lowerASCII(field.Name())] = i
	}
	for c := range aliases {
		for _, name := range aliases[c] {
			if i, ok := byName[name]; ok {
				idx[c] = i
				break
			}
		}
	}
	// Obligatoires : l'horodatage et les quatre prix. Le volume et le côté
	// ask peuvent manquer — un export tiers n'en a pas toujours — et leur
	// absence se lit alors comme une absence, jamais comme un zéro.
	for c, name := range map[int]string{
		colTime: "time", colBidOpen: "open", colBidHigh: "high",
		colBidLow: "low", colBidClose: "close",
	} {
		if idx[c] == -1 {
			return idx, fmt.Errorf("colonne %q absente du fichier", name)
		}
	}
	return idx, nil
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// readInt64Column lit une colonne entière (les horodatages).
//
// Comme pour les prix, une colonne sans valeur absente se lit en bloc ;
// une colonne déclarée nullable — ce que fait pyarrow pour TOUTES ses
// colonnes — passe par les valeurs génériques. Un horodatage ABSENT est
// une erreur et non une valeur par défaut : une bougie sans date n'est
// pas une bougie.
func readInt64Column(chunk parquet.ColumnChunk, dst *[]int64) error {
	pages := chunk.Pages()
	defer pages.Close()
	fast := make([]int64, 4096)
	slow := make([]parquet.Value, 4096)
	for {
		page, err := pages.ReadPage()
		if err != nil {
			return nil // io.EOF compris : la colonne est finie
		}
		values := page.Values()
		if reader, ok := values.(parquet.Int64Reader); ok {
			for {
				n, err := reader.ReadInt64s(fast)
				*dst = append(*dst, fast[:n]...)
				if err != nil || n == 0 {
					break
				}
			}
			continue
		}
		for {
			n, err := values.ReadValues(slow)
			for _, v := range slow[:n] {
				if v.IsNull() {
					return fmt.Errorf("horodatage absent : une bougie sans date n'est pas une bougie")
				}
				if v.Kind() != parquet.Int64 {
					return fmt.Errorf("colonne d'horodatage de type %s, entier attendu", v.Kind())
				}
				*dst = append(*dst, v.Int64())
			}
			if err != nil || n == 0 {
				break
			}
		}
	}
}

// timeUnit dit par combien diviser les entiers de la colonne
// d'horodatage pour obtenir des millisecondes.
//
// Le type logique du fichier fait foi quand il existe : c'est une
// information écrite, pas une supposition. En son absence — un export
// « brut » de certains outils — on déduit l'unité de l'ORDRE DE GRANDEUR,
// ce qui est sûr parce que les plages ne se chevauchent pour aucune date
// plausible : de 1970 à 2100, un horodatage vaut ~10⁹ en secondes,
// ~10¹² en millisecondes, ~10¹⁵ en microsecondes et ~10¹⁸ en
// nanosecondes.
func timeUnit(field parquet.Field, sample int64) (divisor int64, err error) {
	if lt := field.Type().LogicalType(); lt != nil {
		if ts, ok := lt.Value.(*format.TimestampType); ok {
			d := ts.Unit.Value.Duration()
			if d <= 0 || time.Millisecond%d != 0 {
				return 0, fmt.Errorf("unité d'horodatage non exploitable (%s)", ts.Unit.String())
			}
			return int64(time.Millisecond / d), nil
		}
	}
	abs := sample
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs == 0:
		return 1, nil
	case abs < 1e11: // secondes
		return -1000, nil
	case abs < 1e14: // millisecondes
		return 1, nil
	case abs < 1e17: // microsecondes
		return 1000, nil
	default: // nanosecondes
		return 1000000, nil
	}
}

// toMillis applique le diviseur renvoyé par timeUnit. Un diviseur négatif
// signifie « multiplier » : c'est le cas des horodatages en secondes.
func toMillis(v, divisor int64) int64 {
	if divisor < 0 {
		return v * -divisor
	}
	return v / divisor
}

// readDoubleColumn lit une colonne de prix, en distinguant une valeur
// ABSENTE (NULL) d'une valeur nulle. C'est la raison d'être de `present`.
//
// Deux chemins. Une page SANS valeur absente se lit en bloc de float64,
// à la vitesse d'une copie mémoire ; c'est le cas des quatre prix bid et
// du volume, donc de l'essentiel du fichier. Une page qui contient des
// absences passe par les valeurs génériques, qui coûtent un boxing par
// cellule — prix payé seulement là où l'information le justifie.
func readDoubleColumn(chunk parquet.ColumnChunk, dst *[]float64, present *[]bool) error {
	pages := chunk.Pages()
	defer pages.Close()
	fast := make([]float64, 4096)
	slow := make([]parquet.Value, 4096)
	for {
		page, err := pages.ReadPage()
		if err != nil {
			return nil // io.EOF compris : la colonne est finie
		}
		values := page.Values()
		if reader, ok := values.(parquet.DoubleReader); ok && page.NumNulls() == 0 {
			for {
				n, err := reader.ReadDoubles(fast)
				*dst = append(*dst, fast[:n]...)
				for i := 0; i < n; i++ {
					*present = append(*present, true)
				}
				if err != nil || n == 0 {
					break
				}
			}
			continue
		}
		for {
			n, err := values.ReadValues(slow)
			for _, v := range slow[:n] {
				if v.IsNull() {
					*dst = append(*dst, 0)
					*present = append(*present, false)
					continue
				}
				*dst = append(*dst, v.Double())
				*present = append(*present, true)
			}
			if err != nil || n == 0 {
				break
			}
		}
	}
}
