package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogLine est une ligne de journal conservée en mémoire pour la TUI.
type LogLine struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string
}

// String rend la ligne prête pour un panneau de logs.
func (l LogLine) String() string {
	out := fmt.Sprintf("%s %-5s %s", l.Time.Format("15:04:05"), l.Level.String(), l.Message)
	if l.Attrs != "" {
		out += "  " + l.Attrs
	}
	return out
}

// LogBuffer est un tampon circulaire de lignes de journal.
//
// Pourquoi un tampon et pas la sortie standard : dans une TUI, stdout est
// l'écran. Un log écrit directement corromprait l'affichage. Tout passe
// donc ici (et dans le fichier), et la vue « Journal » lit ce tampon.
type LogBuffer struct {
	mu    sync.RWMutex
	lines []LogLine
	max   int
	seq   uint64
}

// NewLogBuffer crée un tampon d'au plus `max` lignes.
func NewLogBuffer(max int) *LogBuffer {
	if max < 1 {
		max = 1
	}
	return &LogBuffer{lines: make([]LogLine, 0, max), max: max}
}

// Append ajoute une ligne, en évinçant la plus ancienne si nécessaire.
func (b *LogBuffer) Append(line LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	if len(b.lines) == b.max {
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
		return
	}
	b.lines = append(b.lines, line)
}

// Lines renvoie une copie des lignes retenues (la plus ancienne d'abord).
func (b *LogBuffer) Lines() []LogLine {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]LogLine, len(b.lines))
	copy(out, b.lines)
	return out
}

// Seq est le compteur total de lignes vues : la TUI s'en sert pour savoir
// si quelque chose a changé sans copier tout le tampon.
func (b *LogBuffer) Seq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.seq
}

// tuiHandler duplique chaque enregistrement slog vers le tampon mémoire
// (et, via le handler enveloppé, vers le fichier).
type tuiHandler struct {
	inner  slog.Handler
	buffer *LogBuffer
	bus    *Bus
	attrs  []slog.Attr
}

func (h *tuiHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *tuiHandler) Handle(ctx context.Context, rec slog.Record) error {
	var sb strings.Builder
	for _, a := range h.attrs {
		fmt.Fprintf(&sb, "%s=%v ", a.Key, a.Value)
	}
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, "%s=%v ", a.Key, a.Value)
		return true
	})
	line := LogLine{
		Time:    rec.Time,
		Level:   rec.Level,
		Message: rec.Message,
		Attrs:   strings.TrimSpace(sb.String()),
	}
	h.buffer.Append(line)
	if h.bus != nil {
		h.bus.Publish(TopicLog, line)
	}
	return h.inner.Handle(ctx, rec)
}

func (h *tuiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &tuiHandler{inner: h.inner.WithAttrs(attrs), buffer: h.buffer, bus: h.bus, attrs: merged}
}

func (h *tuiHandler) WithGroup(name string) slog.Handler {
	return &tuiHandler{inner: h.inner.WithGroup(name), buffer: h.buffer, bus: h.bus, attrs: h.attrs}
}

// Logging regroupe ce que l'application doit garder sous la main pour
// fermer proprement le fichier de journal à l'arrêt.
type Logging struct {
	Logger *slog.Logger
	Buffer *LogBuffer
	file   io.Closer
}

// Close referme le fichier de journal. Sûr à appeler plusieurs fois.
func (l *Logging) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	return f.Close()
}

// SetupLogging branche le journal : fichier (texte, niveau configuré) +
// tampon mémoire pour la TUI + republication sur le bus.
//
// Rien ne part sur stdout/stderr : c'est l'écran de la TUI. Un chemin de
// fichier vide ou inaccessible dégrade proprement vers « mémoire seule »
// plutôt que de faire échouer le démarrage — perdre le fichier de log ne
// justifie pas de refuser de trader.
func SetupLogging(path string, level slog.Level, bufferSize int, bus *Bus) (*Logging, error) {
	buffer := NewLogBuffer(bufferSize)
	var sink io.Writer = io.Discard
	var closer io.Closer
	var openErr error

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			openErr = err
		} else if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			openErr = err
		} else {
			sink, closer = f, f
		}
	}

	inner := slog.NewTextHandler(sink, &slog.HandlerOptions{Level: level})
	logger := slog.New(&tuiHandler{inner: inner, buffer: buffer, bus: bus})
	lg := &Logging{Logger: logger, Buffer: buffer, file: closer}
	if openErr != nil {
		logger.Warn("journal fichier indisponible, journalisation en mémoire seule",
			"chemin", path, "erreur", openErr)
	}
	return lg, nil
}

// ParseLevel convertit "debug"/"info"/"warn"/"error" en slog.Level.
// Une valeur inconnue est une ERREUR : une config fautive doit se voir au
// démarrage, pas se faire remplacer en silence par un défaut.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("niveau de journal inconnu %q (debug, info, warn, error)", s)
	}
}
