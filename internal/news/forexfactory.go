package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// FeedURL : calendrier de la semaine en cours publié par Fair Economy
// (données du calendrier ForexFactory). Constaté le 25 septembre 2026 :
// les semaines précédente et suivante n'y sont PAS servies (404) — d'où
// l'archive.
const FeedURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"

// forexFactory : source « forexfactory ».
type forexFactory struct {
	url    string
	client *http.Client
}

func init() {
	Register("forexfactory", func() Source {
		return &forexFactory{url: FeedURL, client: &http.Client{Timeout: 30 * time.Second}}
	})
}

func (f *forexFactory) Name() string { return "forexfactory" }

func (f *forexFactory) Fetch(ctx context.Context) (Batch, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Batch{}, err
	}
	req.Header.Set("User-Agent", "golden-waterfall")
	resp, err := f.client.Do(req)
	if err != nil {
		return Batch{}, fmt.Errorf("calendrier injoignable : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Batch{}, fmt.Errorf("calendrier : réponse %d de %s", resp.StatusCode, f.url)
	}
	// Borne de lecture : le flux fait une dizaine de kilo-octets ; un
	// corps démesuré n'est pas un calendrier.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Batch{}, err
	}
	events, err := ParseFeedJSON(raw)
	if err != nil {
		return Batch{}, err
	}
	if len(events) == 0 {
		// Un flux vide ne prouve pas une semaine sans annonce.
		return Batch{}, fmt.Errorf("calendrier vide : aucune semaine déclarée couverte")
	}
	return Batch{Events: events, Weeks: WeeksOf(events), Origin: f.url}, nil
}

// feedEvent : format du flux (et de `gw news import` en JSON).
type feedEvent struct {
	Title   string `json:"title"`
	Country string `json:"country"`
	Date    string `json:"date"`
	Impact  string `json:"impact"`
}

// ParseFeedJSON lit le format du flux : une liste de
// {"title", "country", "date" (RFC 3339 avec décalage), "impact"}.
// « country » y est en fait le CODE DE DEVISE (USD, EUR…).
func ParseFeedJSON(raw []byte) ([]Event, error) {
	var in []feedEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("calendrier illisible : %w", err)
	}
	out := make([]Event, 0, len(in))
	for i, e := range in {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Date))
		if err != nil {
			return nil, fmt.Errorf("annonce %d (%q) : date %q illisible : %w", i, e.Title, e.Date, err)
		}
		impact, err := ParseImpact(e.Impact)
		if err != nil {
			return nil, fmt.Errorf("annonce %d (%q) : %w", i, e.Title, err)
		}
		cur := strings.ToUpper(strings.TrimSpace(e.Country))
		if len(cur) != 3 {
			return nil, fmt.Errorf("annonce %d (%q) : devise %q, un code de 3 lettres attendu", i, e.Title, e.Country)
		}
		out = append(out, Event{Time: t.UTC(), Currency: cur, Impact: impact, Title: strings.TrimSpace(e.Title)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// WeeksOf : semaines (débuts) qui contiennent au moins une annonce.
//
// Un import ou un flux ne déclare couvertes QUE ces semaines-là : un
// fichier qui s'arrête le 10 mars ne dit rien du 20.
func WeeksOf(events []Event) []time.Time {
	seen := map[int64]bool{}
	var out []time.Time
	for _, e := range events {
		from, _ := WeekOf(e.Time)
		if k := from.Unix(); !seen[k] {
			seen[k] = true
			out = append(out, from)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
