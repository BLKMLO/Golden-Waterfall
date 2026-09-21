// Package data gère les données de marché historiques : téléchargement
// depuis Dukascopy, stockage local, relecture et ré-échantillonnage.
//
// Le stockage n'utilise pas un format colonne générique mais un format
// binaire maison à enregistrements de taille fixe, `.gwb`. Trois raisons :
//   - aucune dépendance native ni bibliothèque colonne lourde à embarquer,
//     ce qui préserve la promesse « un seul binaire » ;
//   - les prix sont stockés en ENTIERS mis à l'échelle, exactement comme
//     Dukascopy les publie : la conversion est sans perte, et le fichier
//     est deux fois plus petit qu'en float64 ;
//   - à taille d'enregistrement fixe, relire une tranche de dates revient à
//     un seek — pas de décodage de toute l'année pour trois mois.
package data

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

const (
	fileMagic     = "GWB1"
	fileVersion   = uint16(1)
	headerSize    = 64
	recordSize    = 40
	symbolFieldLn = 16
	// noAsk : sentinelle du côté ask absent. Un prix réel n'est jamais nul,
	// la valeur ne peut donc pas être confondue avec une cotation.
	noAsk = int32(0)
)

// FileHeader décrit un fichier d'une année pour un symbole.
type FileHeader struct {
	Symbol string
	Year   int
	// Scale : facteur de mise à l'échelle des prix (10^décimales).
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
}

// Complete indique si l'année a été téléchargée sans aucun jour manquant.
func (h FileHeader) Complete() bool { return h.Failures == 0 }

// WriteSeries écrit une série M1 d'UNE année dans un fichier .gwb.
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

	w := bufio.NewWriterSize(f, 1<<20)
	header.Count = int64(len(series))
	if err := writeHeader(w, header); err != nil {
		return err
	}

	yearStart := time.Date(header.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	scale := float64(header.Scale)
	buf := make([]byte, recordSize)
	for _, bar := range series {
		offset := bar.Time.UTC().Sub(yearStart)
		secs := int64(offset / time.Second)
		if secs < 0 || secs > math.MaxInt32 {
			return fmt.Errorf("bougie %s hors de l'année %d du fichier", bar.Time, header.Year)
		}
		binary.LittleEndian.PutUint32(buf[0:], uint32(int32(secs)))
		putPrice(buf[4:], bar.BidOpen, scale)
		putPrice(buf[8:], bar.BidHigh, scale)
		putPrice(buf[12:], bar.BidLow, scale)
		putPrice(buf[16:], bar.BidClose, scale)
		putPrice(buf[20:], bar.AskOpen, scale)
		putPrice(buf[24:], bar.AskHigh, scale)
		putPrice(buf[28:], bar.AskLow, scale)
		putPrice(buf[32:], bar.AskClose, scale)
		binary.LittleEndian.PutUint32(buf[36:], math.Float32bits(float32(bar.Volume)))
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
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

func putPrice(dst []byte, price, scale float64) {
	if price <= 0 || math.IsNaN(price) {
		binary.LittleEndian.PutUint32(dst, uint32(noAsk))
		return
	}
	binary.LittleEndian.PutUint32(dst, uint32(int32(math.Round(price*scale))))
}

func writeHeader(w io.Writer, h FileHeader) error {
	buf := make([]byte, headerSize)
	copy(buf[0:4], fileMagic)
	binary.LittleEndian.PutUint16(buf[4:], fileVersion)
	sym := h.Symbol
	if len(sym) > symbolFieldLn {
		sym = sym[:symbolFieldLn]
	}
	copy(buf[8:8+symbolFieldLn], sym)
	binary.LittleEndian.PutUint32(buf[24:], uint32(int32(h.Year)))
	binary.LittleEndian.PutUint32(buf[28:], uint32(h.Scale))
	binary.LittleEndian.PutUint64(buf[32:], uint64(h.Count))
	binary.LittleEndian.PutUint32(buf[40:], uint32(h.Failures))
	_, err := w.Write(buf)
	return err
}

// ReadHeader lit l'en-tête sans décoder les bougies (inventaire rapide).
func ReadHeader(path string) (FileHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileHeader{}, err
	}
	defer f.Close()
	return readHeaderFrom(f, path)
}

func readHeaderFrom(f *os.File, path string) (FileHeader, error) {
	buf := make([]byte, headerSize)
	if _, err := io.ReadFull(f, buf); err != nil {
		return FileHeader{}, fmt.Errorf("%s : en-tête illisible (%w)", path, err)
	}
	if string(buf[0:4]) != fileMagic {
		return FileHeader{}, fmt.Errorf("%s n'est pas un fichier Golden Waterfall (.gwb)", path)
	}
	if v := binary.LittleEndian.Uint16(buf[4:]); v != fileVersion {
		return FileHeader{}, fmt.Errorf("%s : format v%d, attendu v%d — retélécharger l'historique", path, v, fileVersion)
	}
	h := FileHeader{
		Symbol:   strings.TrimRight(string(buf[8:8+symbolFieldLn]), "\x00"),
		Year:     int(int32(binary.LittleEndian.Uint32(buf[24:]))),
		Scale:    int32(binary.LittleEndian.Uint32(buf[28:])),
		Count:    int64(binary.LittleEndian.Uint64(buf[32:])),
		Failures: int32(binary.LittleEndian.Uint32(buf[40:])),
	}
	if h.Scale <= 0 {
		return h, fmt.Errorf("%s : échelle de prix invalide (%d)", path, h.Scale)
	}
	return h, nil
}

// ReadSeries relit un fichier .gwb, éventuellement borné à [from, to].
//
// Les bornes sont appliquées par SEEK grâce à la taille fixe des
// enregistrements : lire trois mois ne coûte pas la lecture de l'année.
func ReadSeries(path string, from, to time.Time) (core.Series, FileHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, FileHeader{}, err
	}
	defer f.Close()

	h, err := readHeaderFrom(f, path)
	if err != nil {
		return nil, h, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, h, err
	}
	available := (info.Size() - headerSize) / recordSize
	if available < h.Count {
		// Fichier tronqué (disque plein, interruption d'une ancienne
		// version). On lit ce qui est réellement là et on le DIT.
		return nil, h, fmt.Errorf("%s tronqué : %d bougies annoncées, %d présentes — le retélécharger",
			path, h.Count, available)
	}

	yearStart := time.Date(h.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
	start := int64(0)
	if !from.IsZero() {
		start = seekIndex(f, available, yearStart, from)
	}
	if start >= h.Count {
		return core.Series{}, h, nil
	}
	if _, err := f.Seek(headerSize+start*recordSize, io.SeekStart); err != nil {
		return nil, h, err
	}

	scale := float64(h.Scale)
	r := bufio.NewReaderSize(f, 1<<20)
	out := make(core.Series, 0, h.Count-start)
	buf := make([]byte, recordSize)
	for i := start; i < h.Count; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, h, fmt.Errorf("%s : lecture interrompue à la bougie %d (%w)", path, i, err)
		}
		ts := yearStart.Add(time.Duration(int32(binary.LittleEndian.Uint32(buf[0:]))) * time.Second)
		if !to.IsZero() && ts.After(to) {
			break
		}
		out = append(out, core.Bar{
			Time:     ts,
			BidOpen:  price(buf[4:], scale),
			BidHigh:  price(buf[8:], scale),
			BidLow:   price(buf[12:], scale),
			BidClose: price(buf[16:], scale),
			AskOpen:  price(buf[20:], scale),
			AskHigh:  price(buf[24:], scale),
			AskLow:   price(buf[28:], scale),
			AskClose: price(buf[32:], scale),
			Volume:   float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[36:]))),
		})
	}
	return out, h, nil
}

// seekIndex trouve par dichotomie la première bougie >= from, en lisant
// uniquement l'horodatage de quelques enregistrements.
func seekIndex(f *os.File, count int64, yearStart, from time.Time) int64 {
	target := int32(from.UTC().Sub(yearStart) / time.Second)
	buf := make([]byte, 4)
	lo, hi := int64(0), count
	for lo < hi {
		mid := (lo + hi) / 2
		if _, err := f.ReadAt(buf, headerSize+mid*recordSize); err != nil {
			return lo
		}
		if int32(binary.LittleEndian.Uint32(buf)) < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func price(src []byte, scale float64) float64 {
	v := int32(binary.LittleEndian.Uint32(src))
	if v == noAsk {
		return 0
	}
	return float64(v) / scale
}

// --- Organisation du dossier d'historique ---------------------------------

// FilePath renvoie history/<SYMBOLE>/<SYMBOLE>_m1_<année>.gwb
func FilePath(historyDir, symbol string, year int) string {
	return filepath.Join(historyDir, symbol, fmt.Sprintf("%s_m1_%d.gwb", symbol, year))
}

// fileYear extrait l'année d'un nom de fichier, ou false si le suffixe
// n'en est pas une. Ces fichiers (copies manuelles, renommages) sont
// IGNORÉS, jamais chargés : mieux vaut un trou visible qu'un doublon.
func fileYear(name string) (int, bool) {
	base := strings.TrimSuffix(filepath.Base(name), ".gwb")
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
	entries, err := os.ReadDir(dir)
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
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gwb") {
			continue
		}
		y, ok := fileYear(e.Name())
		if !ok {
			continue
		}
		if !from.IsZero() && y < from.UTC().Year() {
			continue
		}
		if !to.IsZero() && y > to.UTC().Year() {
			continue
		}
		files = append(files, yearFile{year: y, path: filepath.Join(dir, e.Name())})
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
		files, err := os.ReadDir(filepath.Join(historyDir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gwb") {
				continue
			}
			y, ok := fileYear(f.Name())
			if !ok {
				continue
			}
			h, err := ReadHeader(filepath.Join(historyDir, e.Name(), f.Name()))
			if err != nil {
				continue
			}
			inv.Years = append(inv.Years, y)
			inv.Bars += h.Count
			if !h.Complete() {
				inv.Partial = append(inv.Partial, y)
			}
		}
		if len(inv.Years) == 0 {
			continue
		}
		sort.Ints(inv.Years)
		sort.Ints(inv.Partial)
		inv.First = time.Date(inv.Years[0], time.January, 1, 0, 0, 0, 0, time.UTC)
		inv.Last = time.Date(inv.Years[len(inv.Years)-1], time.December, 31, 23, 59, 0, 0, time.UTC)
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}
