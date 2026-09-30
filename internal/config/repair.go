package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Réparation automatique de la configuration (v0.8.2, règle du
// propriétaire du projet) : un réglage qu'une mise à jour rend
// inacceptable — clé disparue, valeur que la nouvelle version refuse,
// révision de moteur retirée — ne bloque plus le démarrage. Il est remis
// sur une valeur valide, et c'est ANNONCÉ :
//
//   - clé inconnue de cette version        → supprimée ;
//   - valeur illisible ou refusée          → valeur par défaut ;
//   - valeur que seul app.New sait juger   → Fix (moteur retiré → sa
//     remplaçante ; moteur, source, passerelle inconnus → défaut) ;
//   - variable GW_* refusée                → ignorée pour cette exécution
//     (le fichier n'y est pour rien, il n'est pas touché).
//
// config.yaml est corrigé EN PLACE — commentaires gardés — après une copie
// de sauvegarde datée à côté de lui. Chaque réparation est rendue dans
// Config.Repairs ; la CLI, les deux interfaces et le journal la montrent.
// Seule une erreur de SYNTAXE YAML reste bloquante : on ne peut pas savoir
// ce que le fichier voulait dire.

// Repair : un réglage remis d'office.
type Repair struct {
	// Key : chemin YAML (« strategy.name »).
	Key string
	// Old, New : valeur refusée et valeur appliquée (« supprimée » pour
	// une clé retirée).
	Old, New string
	// Reason : pourquoi.
	Reason string
	// Env : variable d'environnement ignorée, si la valeur venait d'elle.
	// Le fichier n'est alors pas modifié.
	Env string
}

func (r Repair) String() string {
	if r.Env != "" {
		return fmt.Sprintf("%s=%q ignorée (%s) : %s — %s = %q pour cette exécution",
			r.Env, r.Old, r.Key, r.Reason, r.Key, r.New)
	}
	return fmt.Sprintf("%s : %q → %s (%s)", r.Key, r.Old, r.New, r.Reason)
}

// Problem : un refus de Validate, et les clés qu'il met en cause, dans
// l'ordre où les remettre à leur défaut.
type Problem struct {
	Keys []string
	Msg  string
}

var keyInMessage = regexp.MustCompile(`\b[a-z]+\.[a-z_]+\b`)

// knownPaths : toutes les clés « section.clé » de cette version.
func knownPaths() map[string]bool {
	out := map[string]bool{}
	root := defaultRoot()
	for i := 0; i+1 < len(root.Content); i += 2 {
		sec := root.Content[i+1]
		if sec.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(sec.Content); j += 2 {
			out[root.Content[i].Value+"."+sec.Content[j].Value] = true
		}
	}
	return out
}

// problemsOf : les refus de Validate, avec les clés qu'ils citent.
func problemsOf(msgs []string) []Problem {
	known := knownPaths()
	out := make([]Problem, 0, len(msgs))
	for _, m := range msgs {
		p := Problem{Msg: m}
		for _, k := range keyInMessage.FindAllString(m, -1) {
			if known[k] {
				p.Keys = append(p.Keys, k)
			}
		}
		out = append(out, p)
	}
	return out
}

// defaultRoot : la configuration par défaut, en arbre YAML.
func defaultRoot() *yaml.Node {
	var doc yaml.Node
	raw, _ := yaml.Marshal(Default())
	_ = yaml.Unmarshal(raw, &doc)
	return doc.Content[0]
}

// child : la valeur associée à `key` dans une table YAML (nil sinon).
func child(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func splitPath(path string) (string, string) {
	sec, key, _ := strings.Cut(path, ".")
	return sec, key
}

// setNode place `value` au chemin `path` de la racine, en créant la
// section si besoin.
func setNode(root *yaml.Node, path string, value *yaml.Node) {
	secName, key := splitPath(path)
	sec := child(root, secName)
	if sec == nil {
		sec = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: secName}, sec)
	}
	for i := 0; i+1 < len(sec.Content); i += 2 {
		if sec.Content[i].Value == key {
			// Les commentaires de la ligne d'origine restent en place.
			value.HeadComment, value.LineComment, value.FootComment =
				sec.Content[i+1].HeadComment, sec.Content[i+1].LineComment, sec.Content[i+1].FootComment
			sec.Content[i+1] = value
			return
		}
	}
	sec.Content = append(sec.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// defaultNode : copie de la valeur par défaut de `path`.
func defaultNode(path string) *yaml.Node {
	secName, key := splitPath(path)
	n := child(child(defaultRoot(), secName), key)
	if n == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	}
	return n
}

// summary : une valeur YAML en une ligne, pour les messages.
func summary(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	raw, _ := yaml.Marshal(n)
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}

// decodeStrict décode une racine YAML sur `cfg` en refusant toute clé
// inconnue.
func decodeStrict(root *yaml.Node, cfg *Config) error {
	raw, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && err.Error() != "EOF" {
		return err
	}
	return nil
}

// pruneUnknown retire de la racine les sections et clés que cette
// version ne connaît pas, puis les valeurs illisibles pour leur type
// (remises à leur défaut).
func pruneUnknown(root *yaml.Node) []Repair {
	var out []Repair
	def := defaultRoot()
	kept := root.Content[:0:0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, sec := root.Content[i].Value, root.Content[i+1]
		defSec := child(def, name)
		if defSec == nil {
			out = append(out, Repair{Key: name, Old: summary(sec), New: "supprimée",
				Reason: "section inconnue de cette version"})
			continue
		}
		if sec.Kind != yaml.MappingNode {
			out = append(out, Repair{Key: name, Old: summary(sec), New: "valeurs par défaut",
				Reason: "une section doit être une table"})
			kept = append(kept, root.Content[i], defSec)
			continue
		}
		secKept := sec.Content[:0:0]
		for j := 0; j+1 < len(sec.Content); j += 2 {
			key, val := sec.Content[j].Value, sec.Content[j+1]
			path := name + "." + key
			if child(defSec, key) == nil {
				out = append(out, Repair{Key: path, Old: summary(val), New: "supprimée",
					Reason: "clé inconnue de cette version"})
				continue
			}
			// Type : la valeur seule doit se lire dans le champ.
			probe := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setNode(probe, path, val)
			var scratch Config
			if err := decodeStrict(probe, &scratch); err != nil {
				out = append(out, Repair{Key: path, Old: summary(val), New: summary(child(defSec, key)),
					Reason: "valeur illisible pour ce réglage"})
				val = child(defSec, key)
			}
			secKept = append(secKept, sec.Content[j], val)
		}
		sec.Content = secKept
		kept = append(kept, root.Content[i], sec)
	}
	root.Content = kept
	return out
}

// loadRepaired : décodage, variables d'environnement et validation, en
// réparant ce qui peut l'être. `root` est modifiée en place ; `changed`
// dit si le fichier doit être réécrit.
func loadRepaired(root *yaml.Node, paths Paths) (cfg Config, repairs []Repair, changed bool, err error) {
	repairs = pruneUnknown(root)
	changed = len(repairs) > 0
	ignoredEnv := map[string]bool{}
	reset := map[string]bool{}

	build := func() (Config, error) {
		c := Default()
		if err := decodeStrict(root, &c); err != nil {
			return c, err
		}
		c.Paths = paths
		for _, b := range envBindings() {
			v, ok := os.LookupEnv(b.key)
			if !ok || v == "" || ignoredEnv[b.key] {
				continue
			}
			before := c
			if err := b.apply(&c, v); err != nil {
				ignoredEnv[b.key] = true
				repairs = append(repairs, Repair{Key: b.path, Old: v, New: pathValue(before, b.path),
					Reason: err.Error(), Env: b.key})
				c = before
			}
		}
		return c, nil
	}

	for pass := 0; pass < 20; pass++ {
		if cfg, err = build(); err != nil {
			return cfg, repairs, changed, err
		}
		problems := problemsOf(cfg.problems())
		if len(problems) == 0 {
			return cfg, repairs, changed, nil
		}
		progressed := false
		for _, p := range problems {
			for _, k := range p.Keys {
				if reset[k] {
					continue
				}
				reset[k] = true
				progressed = true
				if env := envFor(k); env != "" && !ignoredEnv[env] {
					// La valeur vient de l'environnement : on l'ignore,
					// le fichier n'y est pour rien.
					ignoredEnv[env] = true
					v, _ := os.LookupEnv(env)
					repairs = append(repairs, Repair{Key: k, Old: v, Reason: p.Msg, Env: env})
					break
				}
				secName, key := splitPath(k)
				repairs = append(repairs, Repair{Key: k, Old: summary(child(child(root, secName), key)),
					New: summary(defaultNode(k)), Reason: p.Msg})
				setNode(root, k, defaultNode(k))
				changed = true
				break
			}
		}
		if !progressed {
			// Plus rien à remettre à son défaut : la configuration reste
			// invalide (cas qui ne devrait pas exister, la configuration
			// par défaut étant valide) — on refuse, comme avant.
			return cfg, repairs, changed, cfg.Validate()
		}
	}
	return cfg, repairs, changed, fmt.Errorf("configuration irréparable après 20 passes")
}

// envFor : la variable GW_* qui force `path`, si elle est posée.
func envFor(path string) string {
	for _, b := range envBindings() {
		if b.path == path {
			if v, ok := os.LookupEnv(b.key); ok && v != "" {
				return b.key
			}
		}
	}
	return ""
}

// pathValue : la valeur courante de `path` dans cfg, telle qu'en YAML.
func pathValue(cfg Config, path string) string {
	var doc yaml.Node
	raw, _ := yaml.Marshal(cfg)
	_ = yaml.Unmarshal(raw, &doc)
	secName, key := splitPath(path)
	return summary(child(child(doc.Content[0], secName), key))
}

// completeRepairs renseigne la valeur appliquée des variables ignorées.
func completeRepairs(cfg Config, repairs []Repair) []Repair {
	for i, r := range repairs {
		if r.Env != "" {
			repairs[i].New = pathValue(cfg, r.Key)
		}
	}
	return repairs
}

const repairHeader = "# Réparé automatiquement par Golden Waterfall"

// writeRepaired sauvegarde l'ancien fichier puis écrit la racine réparée.
func writeRepaired(file string, raw []byte, doc *yaml.Node) (string, error) {
	backup := file + "." + time.Now().Format("2006-01-02T15-04-05") + ".bak"
	if err := os.WriteFile(backup, raw, configPerm); err != nil {
		return "", fmt.Errorf("sauvegarde de %s avant réparation : %w", file, err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return backup, err
	}
	enc.Close()
	// Une seule ligne d'en-tête : celle d'une réparation précédente est
	// remplacée, pas empilée.
	var body bytes.Buffer
	for _, line := range strings.SplitAfter(buf.String(), "\n") {
		if !strings.HasPrefix(line, repairHeader) {
			body.WriteString(line)
		}
	}
	header := repairHeader + " le " + time.Now().Format("2006-01-02 15:04") + " : ancien fichier dans " + backup + "\n"
	return backup, os.WriteFile(file, append([]byte(header), body.Bytes()...), configPerm)
}

// Fix remet un réglage sur une valeur valide quand c'est app.New qui le
// juge (registres de moteurs, de sources, de passerelles, que ce paquet
// ne connaît pas). La réparation est ajoutée à cfg.Repairs et écrite dans
// config.yaml — sauf si la valeur venait d'une variable GW_*, qui est
// alors seulement ignorée pour cette exécution.
func Fix(cfg *Config, path, value, reason string) error {
	old := pathValue(*cfg, path)
	probe := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setNode(probe, path, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	if err := decodeStrict(probe, cfg); err != nil {
		return fmt.Errorf("réparation de %s impossible : %w", path, err)
	}
	r := Repair{Key: path, Old: old, New: value, Reason: reason}
	if env := envFor(path); env != "" {
		r.Env = env
		cfg.Repairs = append(cfg.Repairs, r)
		return nil
	}
	file := cfg.Paths.ConfigFile()
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	setNode(doc.Content[0], path, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	backup, err := writeRepaired(file, raw, &doc)
	if err != nil {
		return err
	}
	if cfg.Backup == "" {
		cfg.Backup = backup
	}
	cfg.Repairs = append(cfg.Repairs, r)
	return nil
}

// RepairKeys : les clés réparées, triées (pour les messages courts).
func RepairKeys(repairs []Repair) []string {
	out := make([]string, 0, len(repairs))
	for _, r := range repairs {
		out = append(out, r.Key)
	}
	sort.Strings(out)
	return out
}
