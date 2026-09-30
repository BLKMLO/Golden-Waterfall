package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BLKMLO/Golden-Waterfall/internal/core"
)

// Source est un fournisseur d'historique M1.
//
// Le téléchargeur (Downloader) ne connaît que ce contrat : il demande une
// année, écrit le fichier, tient les comptes. Tout ce qui est propre à un
// fournisseur — URL, format, découpage (jours, semaines), correspondance
// des symboles, limite de débit — vit dans SA source. Brancher un nouveau
// fournisseur = un fichier `source_<nom>.go`, un `RegisterSource` dans
// un `init()`, et rien d'autre à toucher.
type Source interface {
	// Info décrit la source : ce qu'elle publie et ce qu'elle ne publie
	// pas. L'interface s'en sert pour le dire, pas pour le deviner.
	Info() SourceInfo
	// Serves dit si la source publie ce symbole.
	Serves(symbol string) bool
	// FetchYear récupère l'année demandée, bornée à `now`. Les bougies
	// hors de l'année sont écartées par la source elle-même.
	FetchYear(ctx context.Context, inst Instrument, symbol string, year int,
		now time.Time, step func(Step)) (Fetched, error)
}

// SourceInfo décrit une source.
type SourceInfo struct {
	// Name : clé du registre (`history.source`) et valeur écrite dans la
	// métadonnée `gw.source` de chaque fichier.
	Name string
	// Label : nom lisible.
	Label string
	// FirstYear : première année servie. En demander une plus ancienne ne
	// renverrait que des 404, qu'on prendrait pour un échec.
	FirstYear int
	// Unit : unité de découpage des requêtes ET des manques (« jours »,
	// « semaines »). Le compte des manques d'un fichier est dans cette
	// unité.
	Unit string
	// HasVolume : la source publie-t-elle un volume ? Sinon le volume
	// écrit est NaN (« non mesuré »), jamais zéro.
	HasVolume bool
	// Note : une phrase pour l'écran Données.
	Note string
}

// Step : avancement d'une récupération, dans l'unité de la source.
type Step struct {
	Done, Total int
	Bars        int
	Skipped     int // unités sans donnée publiée (marché fermé, trou de la source)
	Failures    int // unités perdues sur une erreur (réseau, fichier illisible)
	Current     string
}

// Fetched : le résultat d'une année.
type Fetched struct {
	Bars core.Series
	// Missing : unités (au sens de SourceInfo.Unit) où le marché était
	// ouvert et où la source n'a rien donné — erreur réseau ou trou de sa
	// part. C'est le compte écrit dans `gw.failures` : il rend l'année
	// incomplète, donc retéléchargée à la demande suivante.
	Missing int
	// FirstErr : la première erreur rencontrée, pour le journal.
	FirstErr error
}

// ErrNotServed : la source ne publie pas ce symbole. Ce n'est pas une
// panne : un téléchargement de toutes les paires saute celle-ci et
// continue.
var ErrNotServed = errors.New("symbole non publié par cette source")

// SourceOptions : réglages communs, fixés par la configuration.
type SourceOptions struct {
	HTTP        *http.Client
	Concurrency int
	MaxRetries  int
	// BaseURL remplace l'adresse du fournisseur (tests : faux serveur).
	BaseURL string
}

// SourceFactory construit une source.
type SourceFactory func(SourceOptions) Source

var (
	sourcesMu sync.RWMutex
	sources   = map[string]SourceFactory{}
)

// RegisterSource déclare une source ; un doublon est une erreur de
// programmation.
func RegisterSource(name string, f SourceFactory) {
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	if _, ok := sources[name]; ok {
		panic(fmt.Sprintf("source d'historique %q déjà enregistrée", name))
	}
	sources[name] = f
}

// NewSource instancie une source par son nom.
func NewSource(name string, opts SourceOptions) (Source, error) {
	sourcesMu.RLock()
	f, ok := sources[name]
	sourcesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source d'historique inconnue %q. Disponibles : %v", name, SourceNames())
	}
	if opts.HTTP == nil {
		opts.HTTP = newHTTPClient()
	}
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	if opts.MaxRetries < 1 {
		opts.MaxRetries = 5
	}
	return f(opts), nil
}

// SourceNames renvoie les sources enregistrées, triées.
func SourceNames() []string {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	out := make([]string, 0, len(sources))
	for k := range sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DescribeSource renvoie la fiche d'une source sans rien télécharger.
func DescribeSource(name string) (SourceInfo, error) {
	s, err := NewSource(name, SourceOptions{})
	if err != nil {
		return SourceInfo{}, err
	}
	return s.Info(), nil
}

// ServedSymbols partage une liste de symboles entre ceux que la source
// publie et les autres — pour le dire AVANT de lancer une heure de
// requêtes.
func ServedSymbols(src Source, symbols []string) (served, unserved []string) {
	for _, s := range symbols {
		if src.Serves(s) {
			served = append(served, s)
		} else {
			unserved = append(unserved, s)
		}
	}
	return served, unserved
}

// newHTTPClient règle des délais explicites : sans timeout, une connexion
// qui ne répond jamais bloque un worker pour toujours et le
// téléchargement « avance » sans jamais finir.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// errNoData signale un 404 : le fournisseur n'a rien à cette adresse
// (marché fermé, trou de sa part). Le distinguer d'une panne évite de
// compter des « échecs » là où il n'y a rien à télécharger.
var errNoData = errors.New("aucune donnée à cette adresse")

// maxPayload : taille maximale d'une réponse, et d'un fichier une fois
// décompressé. Un jour Dukascopy décompressé tient en 1 440 × 24 octets,
// une semaine FXCM en quelques centaines de kilo-octets : 64 Mo laisse une
// marge immense, et empêche une réponse démesurée (serveur ou proxy
// défaillant, bombe de décompression) de faire tomber le programme faute
// de mémoire au milieu d'un téléchargement de nuit.
var maxPayload int64 = 64 << 20

// errTooLarge : contenu au-delà de maxPayload. Ce n'est pas une panne
// passagère : on ne réessaie pas.
var errTooLarge = errors.New("contenu démesuré, refusé")

// readLimited lit au plus `limit` octets ; au-delà, errTooLarge.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w (plus de %d Mo)", errTooLarge, limit>>20)
	}
	return raw, nil
}

// maxRetryAfter borne l'attente qu'un serveur peut imposer par
// Retry-After : un en-tête à 999 999 secondes suspendait sinon le
// téléchargement pendant onze jours, sans un mot.
const maxRetryAfter = 5 * time.Minute

// httpGet récupère une URL avec retries et backoff.
//
// Deux régimes de backoff, volontairement différents :
//   - 429 (limite de débit) : backoff LONG, Retry-After honoré s'il est
//     fourni, sinon 5 s × tentative. Un backoff court ne fait qu'aggraver
//     la limite ;
//   - autres erreurs réseau / 5xx : backoff exponentiel court (1-2-4-8 s).
func httpGet(ctx context.Context, client *http.Client, url, label string, maxRetries int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "GoldenWaterfall/1.0 (+historique M1)")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		body, readErr := readLimited(resp.Body, maxPayload)
		resp.Body.Close()
		if errors.Is(readErr, errTooLarge) {
			return nil, fmt.Errorf("%s : %w", url, readErr)
		}

		switch {
		case resp.StatusCode == http.StatusNotFound:
			return nil, errNoData
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(resp.Header.Get("Retry-After"), time.Duration(attempt)*5*backoffUnit)
			lastErr = fmt.Errorf("limite de débit %s (429)", label)
			if waitErr := sleepCtx(ctx, wait); waitErr != nil {
				return nil, waitErr
			}
			continue
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("erreur serveur %d", resp.StatusCode)
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("réponse inattendue %d pour %s", resp.StatusCode, url)
		case readErr != nil:
			lastErr = readErr
			if waitErr := sleepCtx(ctx, backoffShort(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("%s : %w (après %d tentatives)", url, lastErr, maxRetries)
}

// backoffUnit : unité des attentes entre tentatives. Les tests la
// réduisent — un faux serveur qui répond 500 ne doit pas coûter quinze
// secondes de suite de tests.
var backoffUnit = time.Second

func backoffShort(attempt int) time.Duration {
	return time.Duration(1<<uint(attempt-1)) * backoffUnit
}

func retryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		if d := time.Duration(secs) * time.Second; secs < int(maxRetryAfter/time.Second) {
			return d
		}
		return maxRetryAfter
	}
	return fallback
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
