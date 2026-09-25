package news

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Archive : un fichier JSON par semaine couverte, nommé par la date de son
// dimanche (heure de New York). Une nouvelle récupération de la même
// semaine REMPLACE le fichier : les annonces sont parfois déplacées ou
// ajoutées en cours de semaine, et la dernière version fait foi.
type Archive struct{ dir string }

// NewArchive ouvre (sans rien créer) l'archive d'un dossier.
func NewArchive(dir string) *Archive { return &Archive{dir: dir} }

// weekFile : contenu d'un fichier de semaine.
type weekFile struct {
	Week      time.Time `json:"week"`
	Origin    string    `json:"origin"`
	FetchedAt time.Time `json:"fetched_at"`
	Events    []Event   `json:"events"`
}

func (a *Archive) path(week time.Time) string {
	return filepath.Join(a.dir, week.In(newYork).Format("2006-01-02")+".json")
}

// Save range un lot semaine par semaine. Renvoie le nombre de semaines
// écrites.
func (a *Archive) Save(b Batch, now time.Time) (int, error) {
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return 0, err
	}
	byWeek := map[int64][]Event{}
	for _, e := range b.Events {
		from, _ := WeekOf(e.Time)
		byWeek[from.Unix()] = append(byWeek[from.Unix()], e)
	}
	for _, week := range b.Weeks {
		wf := weekFile{Week: week, Origin: b.Origin, FetchedAt: now.UTC(), Events: byWeek[week.Unix()]}
		raw, err := json.MarshalIndent(wf, "", "  ")
		if err != nil {
			return 0, err
		}
		// Écriture atomique : un fichier à moitié écrit ne doit pas
		// déclarer couverte une semaine dont il a perdu les annonces.
		tmp := a.path(week) + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			return 0, err
		}
		if err := os.Rename(tmp, a.path(week)); err != nil {
			return 0, err
		}
	}
	return len(b.Weeks), nil
}

// Load relit toute l'archive. Un dossier absent donne un calendrier vide
// (rien de couvert), pas une erreur ; un fichier illisible est une erreur
// qui le nomme.
func (a *Archive) Load() (*Calendar, error) {
	entries, err := os.ReadDir(a.dir)
	if os.IsNotExist(err) {
		return &Calendar{}, nil
	}
	if err != nil {
		return nil, err
	}
	cal := &Calendar{}
	for _, e := range entries {
		// Seuls les fichiers de semaine (AAAA-MM-JJ.json) : un fichier
		// posé là par l'utilisateur n'est pas une semaine couverte.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, err := time.Parse("2006-01-02", strings.TrimSuffix(e.Name(), ".json")); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var wf weekFile
		if err := json.Unmarshal(raw, &wf); err != nil {
			return nil, fmt.Errorf("archive d'actualités : %s illisible : %w", e.Name(), err)
		}
		from, to := WeekOf(wf.Week)
		cal.weeks = append(cal.weeks, span{from, to})
		cal.events = append(cal.events, wf.Events...)
		if wf.FetchedAt.After(cal.lastFetch) {
			cal.lastFetch = wf.FetchedAt
		}
	}
	sort.Slice(cal.weeks, func(i, j int) bool { return cal.weeks[i].from.Before(cal.weeks[j].from) })
	sort.SliceStable(cal.events, func(i, j int) bool { return cal.events[i].Time.Before(cal.events[j].Time) })
	return cal, nil
}
