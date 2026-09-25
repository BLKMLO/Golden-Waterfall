package news

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Options : réglages du service, traduits depuis la section `news` de la
// configuration par app.New (ce paquet n'importe pas config).
type Options struct {
	Enabled   bool
	Source    string
	MinImpact Impact
	Before    time.Duration
	After     time.Duration
	Refresh   time.Duration
	Dir       string
}

// Service tient l'archive, la source et le filtre courant. Un seul, câblé
// dans app.New, partagé par le backtest, le walk-forward et le live.
type Service struct {
	opts    Options
	archive *Archive
	source  Source
	logger  *slog.Logger

	mu          sync.RWMutex
	gate        *Gate
	lastErr     error
	lastAttempt time.Time
}

// NewService construit le service. Une source inconnue est une erreur de
// configuration, découverte au démarrage. L'archive est relue tout de
// suite ; un fichier illisible est signalé mais n'empêche pas de démarrer
// (le filtre est alors inopérant, et c'est dit).
func NewService(opts Options, logger *slog.Logger) (*Service, error) {
	src, err := New(opts.Source)
	if err != nil {
		return nil, fmt.Errorf("news.source : %w", err)
	}
	s := &Service{opts: opts, archive: NewArchive(opts.Dir), source: src, logger: logger}
	if err := s.reload(); err != nil && logger != nil {
		logger.Warn("archive d'actualités illisible : filtre inopérant", "erreur", err)
	}
	return s, nil
}

// Enabled : le filtre est-il actif dans la configuration ?
func (s *Service) Enabled() bool { return s != nil && s.opts.Enabled }

// Gate renvoie le filtre courant, ou nil quand le filtre est désactivé.
// L'objet renvoyé est immuable : un rafraîchissement en publie un autre.
func (s *Service) Gate() *Gate {
	if !s.Enabled() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gate
}

func (s *Service) reload() error {
	cal, err := s.archive.Load()
	if err != nil {
		s.mu.Lock()
		s.gate = &Gate{Calendar: &Calendar{}, Before: s.opts.Before, After: s.opts.After, MinImpact: s.opts.MinImpact}
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.gate = &Gate{Calendar: cal, Before: s.opts.Before, After: s.opts.After, MinImpact: s.opts.MinImpact}
	s.mu.Unlock()
	return nil
}

// Refresh récupère la source, archive, et republie le filtre. Renvoie le
// nombre de semaines écrites.
func (s *Service) Refresh(ctx context.Context) (int, error) {
	s.mu.Lock()
	s.lastAttempt = time.Now()
	s.mu.Unlock()
	batch, err := s.source.Fetch(ctx)
	if err == nil {
		var n int
		n, err = s.archive.Save(batch, time.Now())
		if err == nil {
			err = s.reload()
			s.setErr(err)
			return n, err
		}
	}
	s.setErr(err)
	return 0, err
}

// Import verse un fichier (format du flux, JSON) dans l'archive.
func (s *Service) Import(path string) (int, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	events, err := ParseFeedJSON(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("%s : %w", path, err)
	}
	if len(events) == 0 {
		return 0, 0, fmt.Errorf("%s : aucune annonce, aucune semaine à déclarer couverte", path)
	}
	n, err := s.archive.Save(Batch{Events: events, Weeks: WeeksOf(events), Origin: "import " + path}, time.Now())
	if err != nil {
		return 0, 0, err
	}
	return n, len(events), s.reload()
}

func (s *Service) setErr(err error) {
	s.mu.Lock()
	s.lastErr = err
	s.mu.Unlock()
}

// Run rafraîchit périodiquement jusqu'à l'annulation du contexte. Une
// source « none » ne fait rien.
func (s *Service) Run(ctx context.Context) {
	if !s.Enabled() || s.source.Name() == "none" || s.opts.Refresh <= 0 {
		return
	}
	for {
		if n, err := s.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if s.logger != nil {
				s.logger.Warn("calendrier économique non rafraîchi", "source", s.source.Name(), "erreur", err)
			}
		} else if s.logger != nil {
			s.logger.Info("calendrier économique rafraîchi", "source", s.source.Name(), "semaines", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.opts.Refresh):
		}
	}
}

// Status : ce que l'interface affiche.
type Status struct {
	Enabled     bool
	Source      string
	Weeks       int
	Events      int
	From, To    time.Time
	LastFetch   time.Time
	LastAttempt time.Time
	LastError   string
}

// Status décrit l'état du service.
func (s *Service) Status() Status {
	if s == nil {
		return Status{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{Enabled: s.opts.Enabled, Source: s.source.Name(), LastAttempt: s.lastAttempt}
	if s.lastErr != nil {
		st.LastError = s.lastErr.Error()
	}
	if s.gate != nil {
		c := s.gate.Calendar
		st.Weeks, st.Events, st.LastFetch = c.Weeks(), c.Events(), c.LastFetch()
		st.From, st.To = c.Span()
	}
	return st
}

// Describe : l'état en une phrase.
func (st Status) Describe() string {
	if !st.Enabled {
		return "filtre d'actualités désactivé"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "source %s · %d semaine(s) archivée(s)", st.Source, st.Weeks)
	if st.Weeks > 0 {
		fmt.Fprintf(&b, " du %s au %s", st.From.Format("2006-01-02"), st.To.AddDate(0, 0, -1).Format("2006-01-02"))
	}
	if st.LastError != "" {
		fmt.Fprintf(&b, " · dernière récupération en échec : %s", st.LastError)
	}
	return b.String()
}
