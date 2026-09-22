// Format .gwb — l'ANCIEN stockage, conservé en LECTURE SEULE.
//
// Golden Waterfall écrivait son historique dans un format binaire maison :
// en-tête de 64 octets, enregistrements de 40 octets à taille fixe, prix
// en entiers mis à l'échelle. Il était compact et se relisait par seek,
// mais il n'était lisible que par ce programme — et c'est précisément ce
// qui a fini par coûter cher : impossible d'y verser un historique
// téléchargé ailleurs, impossible de l'ouvrir dans un tableur ou un
// notebook.
//
// Le stockage est passé à Parquet (voir parquet.go). Ce fichier reste
// pour que les gigaoctets déjà téléchargés continuent de se lire, et pour
// que `gw migrate` puisse les convertir. Rien n'écrit plus de .gwb : un
// format qu'on ne peut plus produire ne peut plus se répandre.
package data

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// legacyExt : l'extension de l'ancien format.
const legacyExt = ".gwb"

// isLegacy reconnaît un fichier à convertir plutôt qu'à lire comme du
// Parquet.
func isLegacy(path string) bool { return strings.HasSuffix(path, legacyExt) }

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

// readLegacyHeader lit l'en-tête d'un .gwb sans décoder les bougies.
func readLegacyHeader(path string) (FileHeader, error) {
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

// readLegacySeries relit un .gwb, éventuellement borné à [from, to].
//
// Les bornes sont appliquées par SEEK grâce à la taille fixe des
// enregistrements : lire trois mois ne coûte pas la lecture de l'année.
func readLegacySeries(path string, from, to time.Time) (core.Series, FileHeader, error) {
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
