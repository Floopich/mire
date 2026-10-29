package modem

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Rapport de modem : ce qu'il faut pour prendre en charge un modele, ou
// corriger un firmware, sans avoir l'appareil sous la main. Tout y est lu
// sans rien modifier et passe par Anonymize ou scrub.

// Generic est le choix « Autre modem, non pris en charge » : aucun pilote,
// la sonde Internet seule, et la page Modem propose d'envoyer son rapport.
const Generic = "generique"

// ErrUnsupported dit que le modem de l'abonne n'a pas encore de pilote.
var ErrUnsupported = errors.New("modem non pris en charge")

// IssueURL ouvre le formulaire « Nouveau modem » du depot, a remplir a la
// main.
const IssueURL = "https://github.com/floopich/mire/issues/new?template=nouveau-modem.yml"

// reportIssueURL ouvre une demande libre : son titre et son texte se
// preremplissent sur le site comme dans l'application GitHub. Un modele
// l'emporterait sur le texte fourni, et l'application ignore les formulaires.
const reportIssueURL = "https://github.com/floopich/mire/issues/new?"

// Report est le rapport d'un modem.
type Report struct {
	Mire    string `json:"mire"`
	Date    string `json:"date"`
	Address string `json:"adresse"`
	// Title est le titre de la page d'accueil du modem.
	Title string `json:"titre,omitempty"`
	// Model est le modele choisi dans Mire, s'il y en a un.
	Model string `json:"modele_mire,omitempty"`
	// Name et Software viennent du modem, quand le pilote sait les lire.
	Name     string    `json:"nom,omitempty"`
	Software string    `json:"logiciel,omitempty"`
	Lectures []Capture `json:"lectures"`
}

const (
	uiCallsPath = "interface web : chemins appelés par les scripts"
	homeLimit   = 6000
)

// BuildReport lit la page d'accueil et les scripts du modem, sans
// identifiants ; avec un pilote qui sait le faire, il ajoute ses lectures
// authentifiees.
func BuildReport(ctx context.Context, address string, d Driver, progress Progress) Report {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	r := Report{Date: time.Now().UTC().Format(time.RFC3339), Address: address}
	client := publicClient(10 * time.Second)
	get := func(ctx context.Context, path string) ([]byte, int, http.Header, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+path, nil)
		if err != nil {
			return nil, 0, nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err == nil && resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
		}
		return body, resp.StatusCode, resp.Header, err
	}

	ir, hasInfo := d.(InfoReader)
	cp, hasCapture := d.(Capturer)
	pre := 1
	if hasInfo {
		pre++
	}
	progress.report(0, pre+1, "page d'accueil du modem")
	home, status, header, err := get(ctx, "/")
	c := Capture{Path: "page d'accueil", Status: status}
	if err != nil {
		c.Error = scrub(err.Error())
	}
	if len(home) > 0 {
		r.Title = identify(address, string(home)).Title
		var b strings.Builder
		for _, h := range []string{"Server", "WWW-Authenticate", "Content-Type"} {
			if v := header.Get(h); v != "" {
				fmt.Fprintf(&b, "%s: %s\n", h, v)
			}
		}
		b.WriteString("\n")
		b.WriteString(string(home))
		c.Text = clip(scrub(b.String()), homeLimit)
	}
	r.Lectures = append(r.Lectures, c)

	if hasInfo {
		progress.report(1, pre+1, "informations du modem")
		if info, err := ir.Info(ctx); err == nil {
			r.Name = strings.TrimSpace(info.Manufacturer + " " + info.Model)
			r.Software = info.Software
		}
	}
	if hasCapture {
		caps, err := cp.Capture(ctx, func(done, total int, step string) { progress.report(pre+done, pre+total, step) })
		if err != nil {
			r.Lectures = append(r.Lectures, Capture{Path: "lectures du pilote", Error: scrub(err.Error())})
		}
		r.Lectures = append(r.Lectures, caps...)
		return r
	}
	if len(home) > 0 {
		progress.report(1, 2, "scripts de l'interface web")
		r.Lectures = append(r.Lectures, scriptCalls(ctx, map[string][]byte{"/": home}, func(ctx context.Context, path string) ([]byte, error) {
			b, _, _, err := get(ctx, path)
			return b, err
		})...)
	}
	return r
}

// publicClient lit des pages publiques du modem : certificats auto-signes
// acceptes, pas de proxy, redirections sur le meme appareil seulement.
func publicClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// On ne lit ici que des pages publiques, sans rien envoyer de secret.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           nil,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Hostname() != via[0].URL.Hostname() {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

var (
	scriptSrc = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+)["']`)
	// resourceRef reconnait, entre guillemets, un script ou un gabarit de
	// page : c'est ainsi qu'une interface a page unique charge ses ecrans.
	resourceRef = regexp.MustCompile(`["'](\.?/?[A-Za-z0-9_][A-Za-z0-9_./-]*\.(?:js|html|htm))["']`)
	// apiCall reconnait, entre guillemets, un chemin d'API ou de page
	// dynamique, quel que soit le fabricant.
	apiCall = regexp.MustCompile(`["'](/?(?:api|cgi-bin|goform|data|rest|walk|xml|getter|setter|php)/[A-Za-z0-9_./,=&?-]*|/?[A-Za-z0-9_./-]+\.(?:asp|cgi|php|json|xml|lua|jst|cmd|htm))["']`)
	// keyWords reperent ce qui dit comment lire les canaux et redemarrer.
	keyWords = regexp.MustCompile(`(?i)reboot|restart|\breset\b|docsis|downstream`)
	// restartScreen reconnait un ecran de redemarrage a son nom de fichier.
	restartScreen = regexp.MustCompile(`(?i)reset|reboot|restart`)
	// vendorLib reconnait les bibliotheques generiques : on y suit les
	// liens, mais leurs mots-cles ne disent rien du modem.
	vendorLib = regexp.MustCompile(`(?i)(jquery|chart|sjcl|dragula|nprogress|pushy|tipsy|datatables|ie\.fix|tmpl|\.min\.)`)
)

// scriptCalls parcourt l'interface web du modem, en lecture seule, a partir
// des documents deja lus : scripts, puis gabarits et scripts qu'ils
// chargent. Il en tire les chemins appeles et le contexte des mots utiles.
// Les ecrans de redemarrage sont rendus en entier.
func scriptCalls(ctx context.Context, seeds map[string][]byte, get func(context.Context, string) ([]byte, error)) []Capture {
	const maxFiles, maxSnippets, maxScreen = 80, 40, 12000
	c := Capture{Path: uiCallsPath}
	var screens []Capture
	visited := map[string]bool{}
	var queue, read []string
	enqueue := func(ref string) {
		if strings.Contains(ref, "://") || strings.HasPrefix(ref, "//") {
			return
		}
		if i := strings.IndexAny(ref, "?#"); i >= 0 {
			ref = ref[:i]
		}
		ref = "/" + strings.TrimLeft(strings.TrimPrefix(ref, "./"), "/")
		if !visited[ref] && len(visited) < maxFiles {
			visited[ref] = true
			queue = append(queue, ref)
		}
	}
	calls := map[string]bool{}
	var snippets []string
	seen := map[string]bool{}
	collect := func(src string, body []byte, depth int) {
		// Un menu en JSON echappe ses barres obliques : views\/adm_reset.html.
		body = bytes.ReplaceAll(body, []byte(`\/`), []byte("/"))
		if depth < 2 {
			for _, m := range scriptSrc.FindAllSubmatch(body, -1) {
				enqueue(string(m[1]))
			}
			for _, m := range resourceRef.FindAllSubmatch(body, -1) {
				enqueue(string(m[1]))
			}
		}
		for _, m := range apiCall.FindAllSubmatch(body, -1) {
			calls[strings.TrimRight(string(m[1]), "./,")] = true
		}
		if vendorLib.MatchString(src) {
			return
		}
		covered := 0 // un mot deja dans l'extrait precedent n'en ouvre pas un autre
		for _, loc := range keyWords.FindAllIndex(body, -1) {
			if len(snippets) >= maxSnippets {
				return
			}
			if loc[0] < covered {
				continue
			}
			from, to := max(loc[0]-120, 0), min(loc[1]+120, len(body))
			covered = to
			snippet := src + " : " + scrub(strings.Join(strings.Fields(string(body[from:to])), " "))
			if !seen[snippet] {
				seen[snippet] = true
				snippets = append(snippets, snippet)
			}
		}
	}
	names := make([]string, 0, len(seeds))
	for name := range seeds {
		names = append(names, name)
		visited[name] = true
	}
	sort.Strings(names)
	for _, name := range names {
		collect(name, seeds[name], 0)
	}
	// Deux niveaux : les scripts de la page, puis ce qu'ils chargent.
	for depth := 1; depth <= 2 && len(queue) > 0; depth++ {
		level := queue
		queue = nil
		for _, src := range level {
			if ctx.Err() != nil {
				break
			}
			body, err := get(ctx, src)
			if err != nil {
				continue
			}
			read = append(read, src)
			collect(src, body, depth)
			if restartScreen.MatchString(src) && !vendorLib.MatchString(src) {
				screens = append(screens, Capture{Path: "écran " + src, Text: clip(scrub(string(body)), maxScreen)})
			}
		}
	}
	list := make([]string, 0, len(calls))
	for call := range calls {
		list = append(list, call)
	}
	sort.Strings(list)
	var b strings.Builder
	fmt.Fprintf(&b, "Fichiers lus : %s\n\nChemins appelés :\n%s\n\nAutour de reboot, restart, reset, docsis, downstream :\n%s\n",
		strings.Join(read, ", "), strings.Join(list, "\n"), strings.Join(snippets, "\n"))
	c.Text = clip(b.String(), 32000)
	return append([]Capture{c}, screens...)
}

// DetectedName est le nom que le modem donne de lui-meme, s'il en donne un.
func DetectedName(r Report) string {
	return firstNonEmpty(r.Name, r.Title)
}

// maxIssueURL borne l'adresse de la demande : au-dela d'environ 8 000
// caracteres, GitHub la refuse.
const maxIssueURL = 7500

// IssueLink prepare la demande « Rapport de modem », entierement remplie.
// name est le modem tel que l'abonne le nomme, sinon celui qu'il annonce.
func IssueLink(r Report, name, operator string) string {
	name = firstNonEmpty(strings.TrimSpace(name), DetectedName(r), "modem inconnu")
	v := url.Values{}
	v.Set("title", "Modem : "+name)
	link := ""
	for budget := 6000; budget >= 500; budget -= 250 {
		var b strings.Builder
		fmt.Fprintf(&b, "**Modèle** : %s\n**Logiciel** : %s\n**Opérateur** : %s\n**Adresse** : %s\n\n**Remarques** : \n\n",
			name, firstNonEmpty(r.Software, "inconnu"), firstNonEmpty(operator, "inconnu"), r.Address)
		// Le resume va dans un bloc de code : il ne doit pas le refermer.
		b.WriteString("```text\n" + strings.ReplaceAll(Summary(r, budget), "```", "'''") + "```\n")
		v.Set("body", b.String())
		if link = reportIssueURL + v.Encode(); len(link) <= maxIssueURL {
			return link
		}
	}
	return link
}

// Summary resume le rapport en texte, l'essentiel d'abord, dans la limite
// de budget caracteres : le statut de chaque lecture, puis ce que montre
// l'interface web du modem, puis le detail des reponses (champs simples et
// premier element de chaque table). C'est le texte de la demande.
func Summary(r Report, budget int) string { return render(r, budget, false) }

// Readable rend le rapport complet sous forme lisible : chaque ligne de
// chaque table, et les ecrans de redemarrage en entier. C'est le fichier a
// telecharger.
func Readable(r Report) string { return render(r, 0, true) }

func render(r Report, budget int, full bool) string {
	var lines []string
	add := func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }
	add("Rapport de Mire %s, %s.", firstNonEmpty(r.Mire, "dev"), r.Date)
	if r.Model != "" {
		add("Pilote Mire : %s.", r.Model)
	}
	if r.Name != "" || r.Software != "" {
		add("Modem : %s, logiciel %s.", firstNonEmpty(r.Name, "?"), firstNonEmpty(r.Software, "?"))
	}
	if r.Title != "" {
		add("Adresse : %s, page « %s ».", r.Address, r.Title)
	} else {
		add("Adresse : %s.", r.Address)
	}

	lines = append(lines, "", "Lectures :")
	var ui string
	var details, screens []string
	for _, c := range r.Lectures {
		if c.Path == uiCallsPath {
			ui = strings.TrimSpace(c.Text)
			continue
		}
		head := shortPath(c.Path)
		switch {
		case c.Status != 0:
			head += fmt.Sprintf(" : HTTP %d", c.Status)
		case c.Error != "":
			head += " : " + c.Error
		case strings.HasPrefix(c.Path, "écran "):
			head += " : lu"
		}
		lines = append(lines, "  "+head)
		if strings.HasPrefix(c.Path, "écran ") {
			if full {
				screens = append(screens, "", strings.TrimPrefix(c.Path, "écran ")+" :")
				screens = append(screens, tidy(c.Text)...)
			}
			continue
		}
		var body []string
		switch {
		case len(c.Body) > 0:
			body = outline(c.Body, full)
		case c.Path == "page d'accueil" && c.Text != "":
			hdr, _, _ := strings.Cut(c.Text, "\n\n")
			for _, l := range strings.Split(hdr, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					body = append(body, "  "+l)
				}
			}
		}
		if len(body) > 0 {
			details = append(details, "", shortPath(c.Path)+" :")
			details = append(details, body...)
		}
	}
	if ui != "" {
		// Les extraits d'abord : c'est la que se lit l'appel cherche.
		parts := strings.Split(ui, "\n\n")
		for i := len(parts) - 1; i >= 0; i-- {
			p := strings.TrimSpace(parts[i])
			// Une rubrique vide (« Chemins appelés : » sans rien) n'apprend rien.
			if head, rest, _ := strings.Cut(p, ":"); strings.TrimSpace(rest) == "" || head == "" {
				continue
			}
			lines = append(lines, "", p)
		}
	}
	lines = append(lines, "", "Réponses :")
	lines = append(lines, details...)
	if len(screens) > 0 {
		lines = append(lines, "", "Écrans de redémarrage de l'interface :")
		lines = append(lines, screens...)
	}

	var b strings.Builder
	for _, l := range lines {
		if budget > 0 && b.Len()+len(l)+1 > budget {
			b.WriteString("… (la suite est dans le rapport complet)\n")
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

// tidy rend un texte de page lisible : indentation reduite, lignes vides
// regroupees.
func tidy(s string) []string {
	var out []string
	blank := false
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(strings.ReplaceAll(l, "\t", "  "), " ")
		if strings.TrimSpace(l) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, "  "+l)
	}
	return out
}

// shortPath abrege un chemin a la liste de champs : « /api/v1/system/Hardware… ».
func shortPath(p string) string {
	if i := strings.IndexByte(p, ','); i >= 0 {
		return p[:i] + ",…"
	}
	return p
}

// outline decrit une reponse JSON en quelques lignes : ses champs simples,
// et pour chaque table, son nombre d'elements et le premier, ou chacun en
// version complete.
func outline(body []byte, full bool) []string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		if d, ok := m["data"]; ok && len(m) <= 3 {
			v = d
		}
	}
	var out []string
	var walk func(prefix string, v any, depth int)
	walk = func(prefix string, v any, depth int) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var scalars []string
			for _, k := range keys {
				switch child := t[k].(type) {
				case map[string]any, []any:
					if depth < 2 {
						walk(prefix+k+".", child, depth+1)
					}
				default:
					if k != "__id" {
						scalars = append(scalars, k+"="+short(child))
					}
				}
			}
			if len(scalars) > 0 {
				out = append(out, "  "+prefix+strings.Join(scalars, " ; "))
			}
		case []any:
			name := firstNonEmpty(strings.TrimSuffix(prefix, "."), "liste")
			word := "éléments"
			if len(t) == 1 {
				word = "élément"
			}
			out = append(out, fmt.Sprintf("  %s : %d %s", name, len(t), word))
			n := min(len(t), 1)
			if full {
				n = len(t)
			}
			for i := 0; i < n && depth < 2; i++ {
				walk(fmt.Sprintf("%s n°%d : ", name, i+1), t[i], depth+1)
			}
		default:
			out = append(out, "  "+strings.TrimSuffix(strings.TrimSuffix(prefix, "."), " : ")+" = "+short(t))
		}
	}
	walk("", v, 0)
	return out
}

func short(v any) string {
	s := fmt.Sprint(v)
	if v == nil {
		s = "null"
	}
	return clip(strings.Join(strings.Fields(s), " "), 40)
}
